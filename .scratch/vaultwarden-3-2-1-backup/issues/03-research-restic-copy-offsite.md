# Research: `restic copy` and the two-repo offsite topology

Status: done
Labels: wayfinder:research
Blocked by: (none)

## Question

The 3-2-1 design is: live Docker volume (copy 1), local restic repo on host disk
(copy 2), remote restic repo reached by `restic copy` (copy 3, offsite). Confirm
that topology actually works as intended, and what it costs.

Answer from primary sources:

1. **Two repos, two passwords?** `restic copy` needs credentials for both ends.
   Document the exact env/flag split (`RESTIC_FROM_REPOSITORY`,
   `RESTIC_FROM_PASSWORD`, `--from-repo`, `--from-password-file`). Can a single
   password serve both repos, and is that the recommended arrangement or a
   footgun?
2. **Deduplication across repos.** restic's docs warn that copying between repos
   that do not share a chunker parameter set re-chunks and destroys dedup - what
   exactly is `--copy-chunker-params` on `init`, when must it be used, and what
   goes wrong if the remote repo was initialised without it? This decides
   whether repo creation order matters.
3. **Incrementality.** Does `restic copy` transfer only snapshots the
   destination lacks? How does it decide, and how expensive is the comparison on
   each run?
4. **Retention on two repos.** Does `forget --prune` need to run against the
   remote repo separately, or does `copy` propagate deletions? Assume it does
   not - confirm, and record what the offsite retention story therefore has to
   be.
5. **Bandwidth and cost.** For a repo of a few GB, what does a daily `copy`
   transfer once steady-state? Any per-request cost characteristics worth
   warning a self-hoster about for S3/B2.
6. **Failure modes.** What happens if `copy` is interrupted mid-transfer - is
   the destination left consistent, does it resume, does it leave a lock?
7. **Verifying the offsite copy.** Can `restic check` run against the remote repo
   cheaply, and can `restic snapshots` on the remote confirm the latest snapshot
   arrived without downloading data? This is what feeds the staleness signal.
8. **Alternative worth ruling in or out:** backing up twice (once to each repo)
   instead of `copy`. Note the trade-off - double the backup work and double the
   pause window, against independence of the two repos - so the ticket that
   designs the backup run can decide with evidence.

## Resolution

The topology works. Recommendation at the end of Q8 is **not** `restic copy` for the
daily run - see the reasoning there.

Sources are cited inline as `[docs]` (restic.readthedocs.io, stable),
`[src]` (github.com/restic/restic @ `a80be14`, 2026-08-01), `[help]` (`restic help
<cmd>` from the image), `[test]` (measured here).

Empirical setup for every `[test]` below: `restic/restic:latest`, digest
`sha256:9940e6c7421ab3a21ee9fdc4e91596d04c792b600d31b102696c75cbf3ac4481`,
**restic 0.19.1** (go1.26.4, linux/amd64). Local-disk repos in `/tmp`, restic run
in containers as the host UID with a shared bind-mounted cache dir. Test corpus:
99 MiB (60 MB random + 43 MB compressible text). All temp dirs removed afterwards.

### 1. Two repos, two passwords

`restic copy` copies **from `--from-repo` into `-r`**. The destination is the
*normal* repo flags; the source is the `from-*` group. This is the inverse of most
people's intuition and is the single easiest thing to get backwards.

| role | repo | password |
|---|---|---|
| destination (offsite) | `-r` / `RESTIC_REPOSITORY` / `RESTIC_REPOSITORY_FILE` | `RESTIC_PASSWORD` / `RESTIC_PASSWORD_FILE` / `RESTIC_PASSWORD_COMMAND` |
| source (local) | `--from-repo` / `RESTIC_FROM_REPOSITORY` / `RESTIC_FROM_REPOSITORY_FILE` | `RESTIC_FROM_PASSWORD` / `RESTIC_FROM_PASSWORD_FILE` / `RESTIC_FROM_PASSWORD_COMMAND` |

Also `--from-key-hint` / `RESTIC_FROM_KEY_HINT`, `--from-insecure-no-password`.

Gotchas found:

- **`RESTIC_FROM_PASSWORD` is real but missing from `restic help copy`.** The help
  text lists only `$RESTIC_FROM_PASSWORD_FILE` and `$RESTIC_FROM_PASSWORD_COMMAND`
  [help]. The plain variable *is* in the docs env-var table [docs 075_scripting]
  and in `internal/global/secondary_repo.go:92` (`pwdEnv = "RESTIC_FROM_PASSWORD"`,
  then `resolvePassword(&dstGopts, pwdEnv)`) [src]. Verified [test]: wrong value →
  `Fatal: wrong password or no key found`, exit **12**. Good news for ADR-0013 -
  both passwords can be passed purely through the environment, no argv, no files.
- **No fallback.** `RESTIC_PASSWORD` does *not* serve the source repo. With
  `RESTIC_FROM_REPOSITORY` set and no from-password, restic fails with
  `Fatal: an empty password is not allowed by default. Pass the flag
  '--insecure-no-password' ...`, exit 1 [test]. So a single password serving both
  repos means *setting both variables to the same value*, explicitly.
- Legacy `--repo2` / `RESTIC_REPOSITORY2` / `RESTIC_PASSWORD2` still exist and are
  **mutually exclusive** with the `from-*` group (`Option groups repo2 and
  from-repo are mutually exclusive`) [src]. Don't mix; prefer `from-*`.

**One password or two?** One password, used for both, is the right default.
Distinct passwords buy almost nothing here: the threat 3-2-1 defends against is
host loss / ransomware / fat-fingered delete, and in all of those the attacker who
has the host also has both secrets (they live in the same env). Two passwords
mainly double the "user lost the password → data unrecoverable" surface, which is
the realistic failure mode for a self-hoster. What actually protects the offsite
copy is backend-side immutability (B2 Object Lock, S3 versioning + lifecycle), not
a second passphrase. Not a footgun; the footgun is *forgetting* that both env vars
must be set because there is no fallback.

### 2. Deduplication across repos / `--copy-chunker-params`

Each `restic init` picks a random chunker polynomial; content-defined chunk
boundaries therefore differ per repo. `copy` **does not re-chunk** - it moves blobs
verbatim [help, docs 045]. `init --from-repo <existing> --copy-chunker-params`
copies the existing repo's polynomial into the new repo [help]. *"it is not
possible to change the chunker parameters of an existing repository"* [docs 045].

Measured [test], same 99 MiB corpus:

| destination | init | native backup first | after `copy` from A | delta |
|---|---|---|---|---|
| `repoB_plain` | plain | 60,557,596 B | 121,128,201 B | **+60.6 MB, exactly doubled** (5/5 packs copied) |
| `repoB_params` | `--copy-chunker-params --from-repo A` | 60,571,180 B | 60,572,145 B | **+965 B** (1/1 packs, metadata only) |

**But the important nuance for this design:** the penalty only applies to dedup
against *natively-chunked* data already in the destination. A destination that
**only ever receives copies** is unaffected, because all its blobs came from the
same source chunker. Verified [test]: after adding 3 MB of new data to the source
and copying to both destinations, the deltas were identical -
mismatched 3,539,269 B vs matched 3,539,274 B.

Consequences:

- In a pure copy-only topology, `--copy-chunker-params` is not a correctness
  requirement. Use it anyway - it is one flag and it keeps the **reverse**
  direction cheap (copying remote→local to reseed a rebuilt host), and it is
  strictly required if the remote will ever *also* receive native backups
  (see the Q8 recommendation, where it becomes load-bearing).
- **Order matters:** the local repo must exist first, then the remote is
  `init`-ed from it. Remote enrolment therefore needs *both* credentials at
  enrolment time, even if the daily run later needs only one.
- A remote already init-ed plainly cannot be fixed in place. If it only receives
  copies, leave it. If it needs matching params, the only remedy is re-init and
  re-upload everything.

### 3. Incrementality

Yes, at two levels.

- **Snapshot level.** Every copied snapshot carries an `original` field = the
  source snapshot ID (confirmed in `snapshots --json` [test]). `copy` builds
  `dstSnapshotByOriginal` from *all* destination snapshots, then skips a source
  snapshot when a destination snapshot matches by original **and**
  `similarSnapshots()` - equality of `Time, Tree, Hostname, Username, UID, GID,
  Paths, Excludes, Tags` (everything except `Parent` and `Original`) [src
  `cmd/restic/cmd_copy.go`]. Second run is a silent no-op, 0 bytes; with `-v`:
  `skipping source snapshot ee6f6925, was already copied to snapshot 3879116c` [test].
- **Blob level.** `copyTree`'s `enqueue` adds a blob only when
  `dstRepo.LookupBlobSize(h)` misses [src]. So even a genuinely new snapshot moves
  only blobs the destination lacks.

**Cost of the comparison per run:** `MemorizeList(SnapshotFile)` on both repos
(one LIST each), `LoadIndex` on both repos, and a read of every destination
snapshot file [src]. Measured sizes [test]: index ≈ **59 bytes per blob**
compressed (5,732 B for 97 blobs); snapshot files ≈ **368 B** each. For a few-GB
repo (order 3k-10k blobs) that is ~200-600 KB of index plus a few KB of snapshots
per run. Negligible **with** a persistent cache dir; re-downloaded in full on every
run without one.

Steady-state measurement [test]: +5 MB of new data in the source produced exactly
**four** new objects in the destination - 2 data packs, 1 index, 1 snapshot -
totalling 5.1 MB.

### 4. Retention on two repos

Confirmed: **`copy` never deletes anything.** `forget --keep-last 1 --prune` on the
source left the destination with all 3 snapshots, and the next `copy` simply
skipped them all [test]. `forget`/`prune` must run separately against the remote,
with its own policy and its own schedule.

Implications for the offsite retention story:

- Two independent policies. They may legitimately differ (e.g. shorter local,
  longer offsite) - nothing in restic couples them.
- Remote `prune` takes an **exclusive** lock and must not overlap the copy window.
- Remote `prune` downloads index/metadata and issues deletes; on S3/B2 those are
  billable requests and can trigger early-delete charges on IA/Archive classes.

**Adjacent bug found, affects tickets 02 and 07.** The restic container's hostname
is the container ID, so every run reports a different `Hostname`. `forget`'s
default `--group-by host,paths` then puts every snapshot in its own group and
retention deletes nothing - observed directly: `forget --keep-last 1` reported
`keep 1 snapshots` yet both snapshots survived [test]. The same cause makes every
`backup` print `no parent snapshot found, will read all files`. Fix: pin the
hostname (`restic backup --host`, `RESTIC_HOST`, or `docker run --hostname`).
Note `similarSnapshots` compares `Hostname` too, so an unstable hostname would also
break `copy`'s skip logic if hostnames were ever rewritten - they are not (copy
preserves `Time` and `Hostname` verbatim), so copy itself is safe today.

### 5. Bandwidth and cost

`copy` has **no server-side copy path**: *"This process will have to both download
(read) and upload (write) the entire snapshot(s) due to the different encryption
keys used in the source and destination repository"* [help, docs 045]. Every byte
moved is decrypted and re-encrypted locally.

Steady state = size of the new blobs, plus one index and one snapshot object. With
`repository.DefaultPackSize = 16 * 1024 * 1024` [src], a daily delta smaller than
16 MiB yields ~3 PUTs (1 pack + 1 index + 1 snapshot) plus a lock create and a lock
delete, plus a handful of LISTs. **Single-digit requests per day** - request cost is
noise on S3 and B2 at this scale. (Object counts measured on the local backend,
where one object = one file; the mapping to PUT/GET counts is by inspection, *not*
measured against a real S3/B2 endpoint.)

The real cost drivers, in descending order:

1. **Compressing the staged archive destroys dedup and dominates everything else.**
   Measured [test] on a 20 MB tar with one 500 KB member changed between days:

   | staged format | day 1 added | day 2 added |
   |---|---|---|
   | `.tar` | 19.114 MiB | **981 KiB** |
   | `.tar.gz` | 19.085 MiB | **19.085 MiB** (full re-upload, every day, forever) |
   | `gzip --rsyncable` | 19.105 MiB | 1.951 MiB |

   restic already compresses internally (repo v2 zstd; measured 1.56x on the test
   corpus). Pre-gzipping the tar buys nothing and costs the entire dedup benefit.
   **Flag to ticket 05** - this decision matters more than the copy-vs-backup-twice
   decision.
2. **No cache mount** → both repos' full index re-downloaded every run.
3. **`check` on the remote** *"creates a new temporary cache directory to verify
   data"* by default [help], so it re-downloads all index and tree metadata on
   every run. Pass `--with-cache`.
4. **Interrupted transfers leave data you pay to store** until `prune` (Q6). On
   S3 an aborted multipart upload additionally leaves billable parts that are not
   visible as objects - set an `AbortIncompleteMultipartUpload` lifecycle rule
   (7 days is the common default). For B2 the restic docs recommend a lifecycle
   rule to *"keep only the last version of the file"* [docs 030].

Cold storage: don't. S3 Glacier support is *"still in early alpha stage"* and
restores *"hang from 1 up to 42 hours"* [docs faq]. `copy` is listed as
known-working there, but the restore latency defeats the purpose.

### 6. Failure modes

**Destination consistency: never corrupt.** Snapshots are written only after their
data is uploaded, so an interrupt leaves either nothing or complete *extra* packs -
never a snapshot pointing at missing data. `check` after an interrupted copy
returned `no errors were found`, alongside `2 additional files were found in the
repo, which likely contain duplicate data. This is non-critical, you can run
'restic prune' to correct this.` [test, matches docs faq].

**Resume: effectively no, for a short run.** Verified [test]: SIGINT at 5/6 packs
left 2 packs and **no index file**; the re-run copied 6/6 again and the destination
grew 35 MB → 107 MB. `prune` later reclaimed 32.7 MiB of unreferenced packs.

The mechanism, and the correction to the common "restic resumes" folklore: resume
works only if an index was flushed, and indexes flush at
`indexMaxBlobs = 50000` **or** `indexMaxAge = 10 * time.Minute`
[src `internal/repository/index/index.go:104`]. Both `backup` and `copy` share this.
Control test [test]: a `backup` SIGKILLed after 9 s wrote 69 MB of packs, no index,
and the re-run re-added the full 110.7 MiB - i.e. **`backup` did not resume
either**. So:

- runs shorter than ~10 min: interruption orphans everything uploaded so far, for
  both commands;
- longer runs: checkpoint roughly every 10 min and genuinely resume.

`copy` has one extra constraint: snapshots are committed per *batch* -
`copyTreeBatched` commits once `batchSize >= dstRepo.PackSize()*100` (1.6 GB at
defaults) **and** ≥ 1 minute has elapsed [src]. With a single snapshot per run
there is no intra-snapshot checkpoint at all. This makes the **first** (full,
multi-GB) offsite seed the fragile one; daily deltas are cheap enough not to care.

**Locks.** `copy` takes a *read* (shared) lock on the source and an *append*
(non-exclusive) lock on the destination (`openWithReadLock` / `openWithAppendLock`
[src]).

- SIGINT/SIGTERM → graceful: `signal interrupt received, cleaning up`, exit **130**,
  **both** locks removed, no leftover temp files. Verified twice [test].
- SIGKILL → locks left on **both** repos [test].
- A stale non-exclusive lock does **not** block another `copy` or `backup` - shared
  locks coexist; the retry after SIGKILL succeeded with exit 0 [test]. It **does**
  block `check` and `prune` (exclusive): both failed with exit **11**,
  `repository is already locked by PID 1 on af2a7bdf5513 ... the 'unlock' command
  can be used to remove stale locks`.
- **Containerisation gotcha.** The lock records hostname + PID; inside a container
  that is `PID 1` on a hostname equal to the container ID, so restic's
  "same machine, process gone → stale" heuristic can never fire. Plain
  `restic unlock` (stale-only) **refused** to remove it; only `unlock --remove-all`
  did [test]. Otherwise you wait out `staleLockTimeout = 30 * time.Minute`
  [src `internal/repository/lock_file.go:244`]. Note that pinning the container
  hostname to the host's (desirable for Q4) makes this *worse*, not better: restic
  would then believe it is the same machine and check whether PID 1 is alive - and
  PID 1 on the host always is. Once must handle stale locks explicitly rather than
  relying on restic's heuristic. Ticket 02 owns the `unlock` policy.
- **Invisible garbage, local backend only.** After SIGKILL the local backend left
  `data/be/be70...-tmp-1430709129` files totalling 32 MB. Neither `prune` nor
  `check` removed or reported them [test] - they are invisible to restic and just
  waste disk forever. The object-store analogue is the incomplete multipart upload
  from Q5.

### 7. Verifying the offsite copy

**Staleness signal - cheap, use this.**
`restic snapshots --latest 1 --json --no-lock --no-cache` against the remote reads
only the `snapshots/` listing plus each snapshot file (~368 B); no index, no data.
Measured **0.8 s** with the cache wiped [test]. `--no-lock` means the probe writes
nothing to the remote at all.

It is better than "cheap": because `copy` preserves the source snapshot's `Time`
and records `original` = the source snapshot ID, the remote's latest snapshot time
is *directly* comparable to the local one, and today's local snapshot ID can be
matched exactly against the remote `original` field. That is an unambiguous
"has today's backup landed offsite" check with no data transfer.

**`restic check`** (no `--read-data`) on the remote verifies structural
consistency: loads all indexes, confirms every indexed pack exists, walks every
snapshot's trees and blobs. Metadata only, no data blobs. Exit codes 0 / 1 / 10 /
11 / 12 [help].

Caveats:

- it takes an **exclusive** lock (`create exclusive lock for repository` [test]) -
  it will collide with a concurrent copy/backup, so schedule it, don't overlap it;
- default behaviour builds a fresh temp cache → full metadata re-download every
  run; pass `--with-cache`;
- it does **not** detect bit rot in data packs (that needs `--read-data` or
  `--read-data-subset x%`, which downloads data and costs egress), and it does not
  see the `-tmp-` leftovers from Q6.

So: `snapshots --latest 1 --json --no-lock` daily for the staleness signal;
`check --with-cache` on a slower cadence; `check --read-data-subset` rarely if at all.

### 8. `restic copy` vs backing up twice

The premise in the ticket - *"double the backup work and double the pause
window"* - is only half right. **The pause window is not doubled**, provided the
backup run stages the volume to a file first (as the current design does): the app
pause covers only the volume→staging copy, and both restic invocations then read
the same staged file with the app already running. What actually doubles is CPU
(re-read, re-chunk, re-compress the staged archive), which for a few GB is tens of
seconds to a couple of minutes, off the critical path. Offsite bandwidth is
essentially the same either way - both schemes ship the same new content.

Evidence-backed differences that survive scrutiny:

| | `copy` | backup twice |
|---|---|---|
| app pause | 1x | **1x** (identical, given staging) |
| CPU | 1 chunk+compress pass | 2 passes |
| offsite bytes/day | new blobs only | new blobs only (same order) |
| resume after interrupt | 10-min index checkpoints (Q6) | **same mechanism, same limits** |
| retention | separate `forget --prune` per repo | separate `forget --prune` per repo |
| credentials per process | **both repo passwords in one container** | one password per invocation |
| enrolment | needs local creds to `init` remote with matching chunker params | remote `init` is standalone |
| history backfill when adding a remote later | **entire local history copies in one run** | remote starts empty, fills forward |
| offsite freshness check | exact match on `original` = local snapshot ID | match on tag + time |
| local repo broken (wedged lock, full disk, corruption) | **offsite stops too** | offsite unaffected |

Two things settle it.

First, **`copy` makes the offsite copy structurally downstream of the local repo.**
A stale exclusive lock from a crashed prune, a full disk, or a damaged local repo
takes the offsite path down with it. That dependency is precisely what 3-2-1
exists to remove, and it is not a hypothetical: Q6 shows a SIGKILLed run leaves
locks on both repos, and Q4's hostname bug shows how easily a repo grows without
bound. Backup-twice fails independently on each leg.

Second, **`copy` is the more complicated thing to build and to explain**: the
inverted `-r`/`--from-repo` direction, two password variables with no fallback and
one of them undocumented in `--help`, and a chunker-params ordering constraint at
enrolment. Backup-twice is one code path pointed at two destinations. Per
AGENTS.md's ordering (quality, simplicity, robustness, long-term maintainability
over development cost), the simpler *and* more robust option wins on both counts -
the CPU cost is the only thing on the other side of the ledger, and it is bounded
and off the pause path.

**Recommendation for ticket 07:**

1. **Daily run: back up twice** - one `restic backup` of the staged archive to the
   local repo, one to the remote, sequentially, each with only its own password in
   its environment.
2. **Still `init` the remote with `--copy-chunker-params --from-repo <local>`.**
   One flag, no cost, and it makes the remaining `copy` use case work correctly:
   a one-shot backfill of existing local history when a remote is enrolled after
   the fact, and a one-shot reseed in the reverse direction when rebuilding a host.
   Q2 shows this flag is *load-bearing* for exactly that hybrid - without it, a
   backfill into a remote that also receives native backups double-stores every
   shared byte. This is the reason to keep it, not the copy-only case (where it is
   provably free either way).
3. Run `forget --prune` per repo, and pin the hostname (Q4) or retention silently
   no-ops.
4. Do not gzip the staged archive (Q5, ticket 05) - it costs more than every other
   decision here combined.

### Not verified

- **S3/B2 request counts and costs were not measured against a real endpoint.** All
  object counts come from the local backend. The PUT/GET mapping and the multipart
  lifecycle advice are from AWS/B2 documentation and the restic docs, by inspection.
- Behaviour on repo format version 1 (all tests used the current default, v2 with
  compression). Chunker-param behaviour should be identical but was not exercised.
- Interruption of a **long** (> 10 min) copy, where index checkpointing actually
  engages, was not run - too expensive here. The claim rests on
  `indexMaxAge = 10 * time.Minute` [src] plus the two short-run controls above,
  which behaved exactly as that constant predicts.
- Whether restic's `-tmp-` leftovers can accumulate on non-local backends. The
  `-tmp-` naming is a local-backend implementation detail; the object-store failure
  mode is different (incomplete MPU) and was not reproduced.
- One SIGINT run left a lock on the *source* repo while cleaning the destination's;
  a clean re-run of the same test removed both. Treated as a race in that run, not
  reproduced. If Once relies on graceful shutdown to release locks, it should still
  handle a leftover lock.
