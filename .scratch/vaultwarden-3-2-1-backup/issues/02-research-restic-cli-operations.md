# Research: restic CLI operations and the official container image

Status: done
Labels: wayfinder:research
Blocked by: (none)

## Question

Pin down exactly how Once will drive restic, given that it runs restic in a
container with bind-mounted staging and repo directories, and passes secrets
only through the environment (ADR-0013).

Answer from primary sources (restic.readthedocs.io, the restic repo, the
official `restic/restic` image):

1. **Repo lifecycle.** `restic init` semantics: exit code and message when the
   repo already exists (so Once can be idempotent), repo format version, and
   whether init needs anything beyond `RESTIC_REPOSITORY` + `RESTIC_PASSWORD`.
2. **Backing up a single file.** `restic backup /staging/foo.tar.gz` - what does
   the snapshot path look like, and how does it interact with `--tag`? Confirm
   tags are queryable via `restic snapshots --tag app:vaultwarden --json`.
3. **Secrets via environment.** Full list of env vars Once will set:
   `RESTIC_REPOSITORY`, `RESTIC_PASSWORD` (vs `RESTIC_PASSWORD_FILE` /
   `RESTIC_PASSWORD_COMMAND`), and the backend credential vars for at least S3
   and B2. Confirm nothing forces a password onto argv.
4. **Machine-readable output.** Which subcommands support `--json`, and what is
   the shape of the summary object for `backup` (files, bytes, snapshot id)?
   This is what Once will parse for status reporting.
5. **Exit codes.** The documented exit-code table (0, 1, 3, 10, 11, 12 …) and
   what each means, so Once can distinguish "repo locked" and "wrong password"
   from a real failure.
6. **Locking.** When does restic take a lock, what happens if a previous run
   died holding one, and what does `restic unlock` do? Once runs unattended
   daily, so a stale lock must not wedge backups forever.
7. **`forget` / `prune`.** Exact flag spelling for
   `--keep-daily 7 --keep-weekly 4 --keep-monthly 6`, how `--prune` interacts,
   whether tags scope the policy (`--tag`, `--group-by`), and the cost of prune
   on a repo of a few GB.
8. **`check`.** What `restic check` verifies without `--read-data`, its runtime
   cost, and its exit codes.
9. **The `restic/restic` image.** Entrypoint, what UID it runs as, whether it
   honours `--cache-dir`, where it writes its cache by default (a container
   without a cache mount re-downloads metadata every run - confirm and note the
   cost), and any documented gotchas with bind-mounted repo directories and file
   ownership.
10. **Version pinning.** Current stable version and whether the image publishes
    immutable version tags Once can pin to.

## Resolution

Verified empirically against `restic/restic:latest` = restic **0.19.1**
(image digest `sha256:9940e6c7421ab3a21ee9fdc4e91596d04c792b600d31b102696c75cbf3ac4481`,
built 2026-07-05), plus the v0.19.1 source tree and its `doc/` tree. Throwaway
local repos under `/tmp`, since removed. Doc citations are to paths in
`github.com/restic/restic` at tag `v0.19.1`; they render at
restic.readthedocs.io/en/stable.

### 1. Repo lifecycle

`init` needs nothing beyond `RESTIC_REPOSITORY` + `RESTIC_PASSWORD`. Repo format
version is **2** by default (`--repository-version` defaults to `stable`;
`cat config` returns `{"version":2,...}`). Version 2 is what enables compression.

**`init` is not idempotent.** Re-running against an existing repo:

```
$ restic init
Fatal: Fatal: create repository at /repo failed: config file already exists
EXIT=1
$ restic init --json
{"message_type":"exit_error","code":1,"message":"Fatal: Fatal: create repository at /repo failed: config file already exists"}
EXIT=1
```

Exit **1**, not a distinct code. The doubled `Fatal: Fatal:` is a 0.19.1 cosmetic
bug; do not string-match on it.

Successful init:

```
$ restic init --json
{"message_type":"initialized","id":"f035f496bfbc1de03ad9192ff97e39f0a0d4dd242e95e3397ef550170776d37d","repository":"/repo"}
EXIT=0
```

The documented idempotence pattern (`doc/075_scripting.rst`, "Checking if a
repository is already initialized") is to probe with `cat config` first and
branch on the exit code. Confirmed all three branches:

| probe | exit |
|---|---|
| `cat config`, repo present, right password | 0 |
| `cat config`, dir missing or empty (no `config` file) | 10 |
| `cat config`, repo present, wrong password | 12 |

So: **`cat config` -> 10 means init; 0 means skip; 12 means bail with a password
error.** Do not swallow exit 1 from `init` as "already exists" (it also covers
permission errors, bad backend URLs, etc.).

### 2. Backing up a single file

`restic backup /staging/foo.tar.gz` stores the **absolute in-container path**,
including its parent dirs, as the snapshot tree. `snapshots` reports
`"paths":["/staging/vaultwarden.tar.gz"]`, and `ls` shows two nodes: a `dir`
node `/staging` and the `file` node. Consequences:

- `restic restore <id> --target /out` writes `/out/staging/vaultwarden.tar.gz`
  (verified). To drop the prefix, use the subfolder form
  `restic restore latest:/staging --target /out`, which writes
  `/out/vaultwarden.tar.gz` (verified). `restic dump latest /staging/foo.tar.gz`
  streams the file to stdout byte-exact (verified, 1800000 bytes in = out).
- The staging bind-mount path is baked into every snapshot, so **pick it once and
  never change it**. Changing it changes `paths`, which changes the default
  `forget --group-by host,paths` grouping and silently starts a new retention
  group. Same for the filename: a date-stamped staging filename would make every
  snapshot its own group. Use a **fixed** staging path, e.g. always
  `/staging/vaultwarden.tar.gz`.
- Dedup works as expected: re-backing up an unchanged file gives
  `"files_unmodified":1,"data_added":0` and still creates a new snapshot.

Tags work and are queryable. Verified filter semantics:

- `--tag a --tag b` = OR across flags.
- `--tag a,b` = AND within one flag.
- `restic snapshots --tag app:vaultwarden --json` returns a JSON array; no
  matches returns `[]` with exit 0 (not an error).

### 3. Secrets via environment

Full list is in `doc/075_scripting.rst` "Environment variables". Nothing forces a
password onto argv: `RESTIC_PASSWORD` is read directly, and there is no
positional/flag form for the password itself (`-p/--password-file` and
`--password-command` take a path/command, not the secret).

What Once will set:

| var | purpose |
|---|---|
| `RESTIC_REPOSITORY` | repo location (replaces `-r`) |
| `RESTIC_PASSWORD` | the actual password |
| `RESTIC_CACHE_DIR` | cache dir (see Q9) |
| `RESTIC_HOST` | default for `--host`, both on backup and on filters |
| `RESTIC_COMPRESSION` | `auto`/`off`/`fastest`/`better`/`max`, repo v2 only |
| `RESTIC_PROGRESS_FPS` | status message rate (see Q4) |
| `TMPDIR` | scratch, incl. `check`'s temp cache when `RESTIC_CACHE_DIR` unset |

Alternatives to `RESTIC_PASSWORD`: `RESTIC_PASSWORD_FILE` (path) and
`RESTIC_PASSWORD_COMMAND` (shell command printing to stdout). Per ADR-0013 Once
passes the secret via the environment, so `RESTIC_PASSWORD` is the right one;
the file variant would need a bind-mounted secret file on disk, which is strictly
worse for us. Note that an env var is visible in `docker inspect` and in
`/proc/<pid>/environ` inside the container; that is inherent to the ADR-0013
choice, not a restic constraint.

Empty password is rejected: `Fatal: an empty password is not allowed by default.
Pass the flag --insecure-no-password ...`, exit 1.

Backend credentials (verbatim from `doc/075_scripting.rst`):

```
AWS_ACCESS_KEY_ID                   Amazon S3 access key ID
AWS_SECRET_ACCESS_KEY               Amazon S3 secret access key
AWS_SESSION_TOKEN                   Amazon S3 temporary session token
AWS_DEFAULT_REGION                  Amazon S3 default region
AWS_PROFILE                         Amazon credentials profile (alternative to specifying key and region)
AWS_SHARED_CREDENTIALS_FILE         Location of the AWS CLI shared credentials file (default: ~/.aws/credentials)
RESTIC_AWS_ASSUME_ROLE_ARN          Amazon IAM Role ARN to assume using discovered credentials
RESTIC_AWS_ASSUME_ROLE_SESSION_NAME Session Name to use with the role assumption
RESTIC_AWS_ASSUME_ROLE_EXTERNAL_ID  External ID to use with the role assumption
RESTIC_AWS_ASSUME_ROLE_POLICY       Inline Amazon IAM session policy
RESTIC_AWS_ASSUME_ROLE_REGION       Region to use for IAM calls for the role assumption (default: us-east-1)
RESTIC_AWS_ASSUME_ROLE_STS_ENDPOINT URL to the STS endpoint (default is determined based on RESTIC_AWS_ASSUME_ROLE_REGION). You generally do not need to set this, advanced use only.

B2_ACCOUNT_ID                       Account ID or applicationKeyId for Backblaze B2
B2_ACCOUNT_KEY                      Account Key or applicationKey for Backblaze B2
```

For S3 the region/endpoint otherwise comes from the repo URL
(`s3:https://host/bucket`), not from a separate env var. For any S3-compatible
provider (R2, Wasabi, Minio) the same `AWS_*` pair is used.

Also available if we ever need them: `AZURE_ACCOUNT_NAME`/`AZURE_ACCOUNT_KEY`/
`AZURE_ACCOUNT_SAS`, `GOOGLE_PROJECT_ID`/`GOOGLE_APPLICATION_CREDENTIALS`,
`RESTIC_REST_USERNAME`/`RESTIC_REST_PASSWORD`, `RCLONE_BWLIMIT`, and the
`OS_*` OpenStack Swift set.

**Not verified empirically:** the S3 and B2 paths. No credentials available here;
those names come from the docs only.

### 4. Machine-readable output

`--json` is supported by: `backup`, `cat`, `check`, `diff`, `find`, `forget`,
`init`, `key list`, `ls`, `restore`, `snapshots`, `stats`, `tag`, `version`
(`doc/075_scripting.rst`). **`prune` is not on that list**, and this is real:
`restic prune --json` printed the ordinary human progress text verbatim. Do not
try to parse prune output. Only its `exit_error` is JSON.

Fatal errors emit a final JSON line **on stderr**:
`{"message_type":"exit_error","code":<int>,"message":"<string>"}`.

`backup --json` uses JSON lines. Actual output for a single-file backup:

```
{"message_type":"summary","files_new":1,"files_changed":0,"files_unmodified":0,"dirs_new":1,"dirs_changed":0,"dirs_unmodified":0,"data_blobs":3,"tree_blobs":2,"data_added":3000826,"data_added_packed":3000978,"total_files_processed":1,"total_bytes_processed":3000000,"total_duration":0.739042151,"backup_start":"2026-08-13T05:46:16.272066594Z","backup_end":"2026-08-13T05:46:17.011108745Z","snapshot_id":"707fd0a594eb6c1d60571b12326e10694a8d2916f5bd2e686d0a6877d50233d7"}
```

Fields Once wants: `snapshot_id` (**full 64-hex, not the short id**),
`total_bytes_processed`, `data_added` / `data_added_packed` (the incremental cost
after dedup+compression), `total_duration`, `backup_start`/`backup_end`.
`dry_run` (bool) appears only under `--dry-run`. Per the docs, "If a field
contains a default value like `0` or `""`, it may be omitted from the JSON
output" - so treat every field as optional when decoding.

`status` lines are interleaved before the summary. They **are** emitted when
stdout is a pipe, contrary to what a short test suggests (a sub-second backup
just finishes first). Verified with a slow source:

```
{"message_type":"status","seconds_elapsed":1,"seconds_remaining":5764188709209,"percent_done":0,"total_files":1,"bytes_done":3522604,"current_files":["/slow"]}
```

Default rate is ~10/s and `RESTIC_PROGRESS_FPS` overrides it (`0.0166` gives
roughly one per minute, useful for logs). **Once must stream and take the last
`summary` line, not read the first line.** Setting `RESTIC_PROGRESS_FPS` low, or
`--quiet` (which suppresses status but keeps the summary), keeps the volume sane.
Note `seconds_remaining` is garbage for stdin-sourced backups (no known total);
do not surface it.

Other useful shapes, all verified:

```
$ restic snapshots --json     # single JSON array
[{"time":...,"parent":...,"tree":...,"paths":[...],"hostname":...,"username":...,
  "tags":[...],"program_version":"restic 0.19.1","summary":{...},
  "id":"<64hex>","short_id":"<8hex>"}]

$ restic check --json
{"message_type":"summary","num_errors":0,"broken_packs":null,"suggest_repair_index":false,"suggest_prune":false}

$ restic version --json
{"message_type":"version","version":"0.19.1","go_version":"go1.26.4","go_os":"linux","go_arch":"amd64"}

$ restic stats --json
{"total_size":3400000,"total_file_count":4,"snapshots_count":2}

$ restic restore latest --target /out --json
{"message_type":"summary","total_files":2,"files_restored":2,"total_bytes":1800000,"bytes_restored":1800000}
```

`forget --json` emits a single array of **groups**, each
`{"tags","host","paths","keep":[<snapshot>...],"remove":[<snapshot>...]|null,
"reasons":[{"snapshot":{...},"matches":["daily snapshot",...]}]}`. It embeds the
full snapshot object three times per snapshot, so it is bulky (8.5 KB for 9
snapshots in one group). Parse `remove`/`keep` short ids and discard the rest.

### 5. Exit codes

Table verbatim from `doc/075_scripting.rst`:

```
+-----+----------------------------------------------------+
| 0   | Command was successful                             |
| 1   | Command failed, see command help for more details  |
| 2   | Go runtime error                                   |
| 3   | `backup` could not read some source data, or       |
|     | `forget` could not remove one or more snapshots    |
| 10  | Repository does not exist (since restic 0.17.0)    |
| 11  | Failed to lock repository (since restic 0.17.0)    |
| 12  | Wrong password (since restic 0.17.1)               |
| 130 | Command was cancelled (e.g. SIGINT or SIGTERM)     |
+-----+----------------------------------------------------+
```

The docs carry a warning Once must honour: "New exit codes will be added over
time. If an unknown exit code is returned, then it MUST be treated as a command
failure."

Empirically confirmed on 0.19.1, with the matching stderr JSON:

| command | condition | exit | stderr JSON |
|---|---|---|---|
| `snapshots` / `cat config` | wrong password | **12** | `{"message_type":"exit_error","code":12,"message":"Fatal: wrong password or no key found"}` |
| `snapshots` / `cat config` | repo dir missing or has no `config` | **10** | `{"message_type":"exit_error","code":10,"message":"Fatal: repository does not exist: unable to open config file: stat /nope/config: no such file or directory\nIs there a repository at the following location?\n/nope"}` |
| `prune` | repo already locked | **11** | `{"message_type":"exit_error","code":11,"message":"unable to create lock in backend: repository is already locked by PID 1 on 7389c167df85 by root (UID 0, GID 0)\nlock was created at ...\nstorage ID 42da77f5\nthe `unlock` command can be used to remove stale locks"}` |
| `init` | repo already exists | **1** | `{"message_type":"exit_error","code":1,"message":"Fatal: Fatal: create repository at /repo failed: config file already exists"}` |
| `snapshots` | no password set | **1** | `Fatal: an empty password is not allowed by default...` |

Mapping for Once: 10 -> "not set up yet", 11 -> "busy, retry later, not an
alert", 12 -> "wrong/missing repo password, hard config error, alert loudly",
3 -> "partial success, snapshot exists but incomplete", 0 -> ok, anything else
-> failure.

Exit 3 for `backup` was **not** reproduced here (needs an unreadable source
file); it is documented and also stated in `restic backup --help`.

### 6. Locking

Lock classes, from `cmd/restic/*.go` (`openWithReadLock` / `openWithAppendLock` /
`openWithExclusiveLock`):

| lock | commands |
|---|---|
| read (non-exclusive) | `snapshots`, `ls`, `restore`, `dump`, `cat`, `find`, `diff`, `stats`, `mount`, `copy` (source) |
| append (non-exclusive) | **`backup`**, `copy` (destination), `key add` |
| **exclusive** | **`forget`**, **`prune`**, **`check`**, `tag`, `rewrite`, `repair *`, `recover`, `migrate`, `key passwd`, `key remove` |

Rule (`internal/repository/lock_file.go`, `checkForOtherLocks`): "If an exclusive
lock is to be created, checkForOtherLocks returns an error if there are any other
locks, regardless if exclusive or not. If a non-exclusive lock is to be created,
an error is only returned when an exclusive lock is found." Verified: a leftover
non-exclusive lock does **not** block `backup`, but does block `prune` with
exit 11.

A live restic refreshes its lock every 5 min (`defaultRefreshInterval`), so a
long-running command never looks stale.

Staleness (`internal/repository/lock_file.go`, verbatim):

```go
var StaleLockTimeout = 30 * time.Minute

// Stale returns true if the lock is stale. A lock is stale if the timestamp is
// older than 30 minutes or if it was created on the current machine and the
// process isn't alive any more.
```

The host check is `os.Hostname() != l.Hostname -> not stale`; the liveness check
(`lock_file_unix.go`) sends `SIGHUP` to the recorded PID.

**This is the big gotcha for Once, and it bites in both directions.** Verified by
SIGKILLing a container that held a lock:

- **Random container hostname** (Docker default): the next run's hostname differs
  from the dead run's, so restic short-circuits to "not stale". `restic unlock`
  exits 0 and removes nothing. The lock persists for the full 30 min.
- **Fixed `--hostname once-restic`**: worse. The hostname now matches, so restic
  proceeds to the PID check. The dead lock recorded `PID 1` (container init), and
  the *new* container's own restic is also PID 1 in its own PID namespace, so
  `SIGHUP 1` succeeds and the lock is judged **alive**. Verified: `unlock` again
  exits 0, removes nothing.

So neither hostname strategy lets restic self-heal a lock left by a killed
container inside 30 minutes. `restic unlock` alone is **not** sufficient recovery.

What does work, verified:

```
$ restic unlock --remove-all
successfully removed 1 locks
EXIT=0
```

Recommendation: Once owns the whole schedule for a given repo, so before a
maintenance run (`forget`/`prune`/`check`) it can assert no restic container of
its own is running and then issue `unlock --remove-all`. That is safe precisely
because Once is the single writer; it would be unsafe on a shared repo. Also pass
`--retry-lock <dur>` (default: no retries) so a legitimately concurrent run waits
instead of failing; verified that it still exits 11 after the timeout.

Set a stable `--hostname` on the container anyway, for the reason in Q9, and set
`--host` / `RESTIC_HOST` explicitly for snapshot grouping.

`restic list locks` shows lock IDs; `restic unlock --help` documents exit 0 on
success and 1 on any error, and notes it "works even with repositories served in
append-only mode from restic's rest-server".

### 7. `forget` / `prune`

Exact spelling confirmed from `restic forget --help`:

```
-d, --keep-daily n      -w, --keep-weekly n      -m, --keep-monthly n
-l, --keep-last n       -H, --keep-hourly n      -y, --keep-yearly n
    --keep-within duration     --keep-within-{hourly,daily,weekly,monthly,yearly} duration
    --keep-tag taglist
```

So `--keep-daily 7 --keep-weekly 4 --keep-monthly 6` is right as written.

Scoping:

- `--tag tag[,tag,...]` (repeatable) **filters which snapshots the policy
  considers**, same AND/OR semantics as Q2. `--host` and `--path` likewise.
- `-g, --group-by` **defaults to `host,paths`** and partitions the considered
  snapshots; the `--keep-*` counts apply **per group**. `--group-by ''` disables
  grouping; `tags` can be added.
- **Both matter and they interact.** Since Once backs up one fixed path per app
  and tags with `app:<name>`, filtering by `--tag app:vaultwarden` plus the
  default `host,paths` grouping yields exactly one group, which is what we want.
  But it only holds if hostname and staging path are stable (Q2, Q9). A random
  container hostname would produce one group per run and the policy would keep
  everything forever, silently. **Always pass `--host`/`RESTIC_HOST` explicitly.**
- Safety net documented in the help: "If there are not enough snapshots to keep
  one for each duration related `--keep-{within-,}*` option, the oldest snapshot
  in the group is kept additionally." Also `--unsafe-allow-remove-all` is required
  to empty a group, so a misconfigured policy cannot silently delete everything.
- `--dry-run` works with `--json` and reports the same `keep`/`remove`/`reasons`
  shape.

`--prune` "automatically run[s] the 'prune' command if snapshots have been
removed". Verified with `forget --keep-daily 2 ... --prune --json`: stdout
carried only the forget JSON array; the prune stage produced no output at all
under `--json`, yet the data really was removed. Treat the combined command as
opaque and check the exit code. If Once wants prune telemetry, run `forget` and
`prune` as two commands.

Cost. Docs (`doc/060_forget.rst`): "Pruning snapshots can be a time-consuming
process... During a prune operation, the repository is locked and backups cannot
be completed." And on repacking: "restic must download the file from the
repository storage and re-upload the needed data in the repository. This can be
very time-consuming for remote repositories."

Measured locally, 8 snapshots of a 250 MB file mutated ~20% per snapshot,
repo 602 MB on SSD, `forget --keep-last 2` then `prune`:

```
to repack:           128 blobs / 169.497 MiB
this removes:         81 blobs / 108.161 MiB
to delete:           138 blobs / 188.593 MiB
total prune:         219 blobs / 296.754 MiB
remaining:           204 blobs / 304.305 MiB
unused size after prune: 12.305 MiB (4.04% of remaining size)
prune wall=1.60 s
```

Repo went 602 MB -> 305 MB. Local disk prune is I/O bound and trivially fast.
The number that matters is **`to repack`**: on a remote backend that is a full
download + re-upload of ~170 MB, plus the deletes. So for a few-GB repo expect
prune to move on the order of a few hundred MB over the network, i.e. minutes on
a home uplink, during which backups are blocked. Levers, all documented:

- `--max-unused limit` (default `5%`) trades repo slack for repack work;
  `--max-unused unlimited` "will only remove partly used pack files when
  you want to minimize the time and bandwidth used by the prune operation".
- `--max-repack-size size` caps repack volume per run, so prune can be
  incremental.
- `--repack-cacheable-only` repacks metadata only, "allows a very fast repacking".
- `--dry-run` to preview.

For Once: run prune on a **weekly/monthly** cadence, not with every daily backup,
and never concurrently with a backup window.

### 8. `check`

Without `--read-data`, `check` verifies **structural** consistency only: it loads
and validates all index files, verifies every pack referenced by the index exists
with the expected size, and walks all snapshots, trees and blob references. It
does **not** read or hash pack contents, so on-disk bitrot inside a pack is not
detected. Docs: "By default, check verifies the structural consistency and
integrity of snapshots, trees and pack files. To also verify the integrity of the
actual backed-up data, use the --read-data or --read-data-subset flags."

Exit codes, verbatim from `restic check --help`:

```
Exit status is 0 if the command was successful.
Exit status is 1 if there was any error.
Exit status is 10 if the repository does not exist.
Exit status is 11 if the repository is already locked.
Exit status is 12 if the password is incorrect.
```

Note there is **no distinct code for "repo is corrupt"**: damage reports exit 1,
same as a transport error. Once must read the JSON summary to tell them apart:

```
$ restic check --json
{"message_type":"summary","num_errors":0,"broken_packs":null,"suggest_repair_index":false,"suggest_prune":false}
```

`suggest_repair_index` / `suggest_prune` / `broken_packs` are the actionable
signals.

Cost. Measured on the 602 MB / 8-snapshot repo above: plain `check` **1.01 s**,
`check --read-data` **1.44 s** (local SSD, so both are effectively free here).
The structural check scales with index and tree size, not repo size, so it stays
cheap even on a remote backend: it downloads indexes plus tree blobs and issues a
listing. `--read-data` downloads every pack, i.e. the whole repo. Docs: "beware
that it might incur higher bandwidth costs than usual and also that it takes more
time than the default check."

`check` **takes an exclusive lock** (`cmd_check.go:245`), so it blocks backups
exactly like prune. Plan it in the same maintenance window.

`check` also creates a **fresh temporary cache** by default, so it re-downloads
metadata even when a warm cache exists. Verified in the run output:
`using temporary cache in /cache/restic-check-cache-1148497196`. Use
`--with-cache` to reuse the real cache. If `RESTIC_CACHE_DIR` is unset the temp
cache goes under `TMPDIR` instead.

`--read-data-subset=n/t` or `=x%` lets Once amortise deep verification, e.g.
`--read-data-subset=10%` after each backup covers the repo over ~10 runs. That is
the recommended shape for an unattended daily.

### 9. The `restic/restic` image

Verified via `docker inspect` and the `docker/` dir in the source tree.

- **Entrypoint** is `["/entrypoint.sh"]`, no `Cmd`. The script is a 6-line
  busybox-sh wrapper that execs `/usr/bin/restic "$@"`, optionally under
  `ionice`/`nice`:

  ```sh
  set -- /usr/bin/restic "$@"
  if [ -n "${IONICE_CLASS}" ]; then
      set -- ionice -c "${IONICE_CLASS}" -n "${IONICE_PRIORITY:-4}" "$@"
  fi
  exec nice -n "${NICE:-0}"  "$@"
  ```

  So Once passes restic args directly as the container command. Image env
  `IONICE_CLASS=` (empty), `IONICE_PRIORITY=4`, `NICE=0`. Setting `NICE=10` and
  `IONICE_CLASS=2` is a cheap way to keep a nightly backup off the app's back.
  `exec` is used, so restic is PID 1 and receives `docker stop`'s SIGTERM
  directly -> clean shutdown and exit 130.

- **UID**: no `User` in the image config, so it runs as **root (uid 0, gid 0)**.
  Base is `alpine:latest`; it also installs `ca-certificates fuse openssh-client
  tzdata jq`. Multi-arch: `linux/386`, `linux/amd64`, `linux/arm/v7`,
  `linux/arm64` (OCI image index).

- **Bind-mount ownership is a real problem.** Running as root, every file restic
  writes into a bind-mounted repo is `root:root` and restrictive:

  ```
  -r--------   1 root root  155 config
  drwx------ 258 root root      data/
  drwx------   2 root root      index/  keys/  locks/  snapshots/
  ```

  The host user cannot even `ls` the repo afterwards. And you cannot simply fix
  it with `--user`: verified that `--user 1000:1000` against a root-created repo
  fails with `List(key) failed: open /repo/keys: permission denied`, exit 1, and
  creating a fresh repo as uid 1000 in a root-owned dir fails with
  `mkdir /repo2/data: permission denied`. **Decide the uid once, up front, and
  keep it forever**; mixing uids across runs wedges the repo. Simplest is to let
  it run as root (Once already manages Docker, so it has root-equivalent
  authority) and accept root-owned repo files. If Once instead runs `--user`, it
  must also pre-create and `chown` the repo dir, the cache dir, and `$HOME`, and
  set `HOME` to something writable (the image sets `HOME=/root`).

- **Cache.** `--cache-dir` and `RESTIC_CACHE_DIR` are both honoured. Default in
  this image is `$HOME/.cache/restic` = **`/root/.cache/restic`**, confirmed:

  ```
  $ restic cache
  no cache dirs found, basedir is /root/.cache/restic
  ```

  which lives in the container's writable layer and **dies with `--rm`**. So yes:
  a container without a cache mount rebuilds cache from scratch every run.
  Confirmed the cache *is* populated even for a local-filesystem repo (it is not
  remote-only), under `<cachedir>/<repo-id>/{index,data,snapshots,version}`.

  **The cost is small for Once's workload.** For the 602 MB / 8-snapshot repo the
  whole cache was **33.4 KiB** (60K data + 56K index + 44K snapshots on disk), and
  the repo's own `index/` was 16 KB against 305 MB of `data/`. Because Once stores
  one big tarball per snapshot, there are very few trees and blobs, so metadata
  stays in the tens of KiB and scales with snapshot count, not data volume. A
  cold cache costs a handful of small GETs, not a metadata re-download of any
  consequence. Mount a cache volume anyway (it is free and keeps `restic check
  --with-cache` and future many-file profiles cheap), but this is not a
  correctness issue and not worth blocking on. Note the cache dir is also
  root-owned 0700, same caveat as above.

- **Hostname**, from the image's own `docker/README.md`, verbatim:

  > Restic relies on the hostname for various operations. Make sure to set a
  > static hostname using `--hostname` when creating a Docker container,
  > otherwise Docker will assign a random hostname each time.

  This is upstream telling us exactly the Q6/Q7 hazard. Once should pass
  `--hostname` on the container **and** `--host`/`RESTIC_HOST` to restic. They are
  different things: `--host` sets the snapshot's `hostname` field (grouping,
  filtering), while the container hostname is what lands in **lock files** and
  drives stale-lock detection. Set both, and still rely on
  `unlock --remove-all` for recovery, since Q6 shows a fixed hostname does not
  actually make stale detection work under Docker's PID namespace.

### 10. Version pinning

Current stable is **0.19.1**, released 2026-07-05
(`github.com/restic/restic` latest release = `v0.19.1`; image built the same day;
`restic version` reports `restic 0.19.1 compiled with go1.26.4 on linux/amd64`).

Docker Hub `restic/restic` publishes **exact patch tags with no `v` prefix**:
`0.19.1`, `0.19.0`, `0.18.1`, `0.18.0`, `0.17.3` ... back to `0.10.0`, plus
`latest`. 35 tags total. There are **no floating `0.19` / `0` tags and no
variant tags** (`-alpine` etc.).

Caveat: the tags are conventionally immutable but not *technically* immutable, and
`0.19.1` and `latest` currently resolve to **different index digests**
(`sha256:136600b6ff6843d61d...` vs `sha256:9940e6c7421ab3a21e...`) even though
their per-platform manifests are byte-identical (`amd64` is
`sha256:08916bcda4a44...` under both); only the attestation entries differ. So
comparing `RepoDigests` across tags is misleading.

Recommendation: pin `restic/restic:0.19.1` in config, and additionally record the
resolved image digest so upgrades are explicit. Do not use `latest`.

### Suggested command shapes for Once

```
docker run --rm --hostname once-restic \
  -e RESTIC_REPOSITORY=... -e RESTIC_PASSWORD=... -e RESTIC_HOST=once \
  -e RESTIC_CACHE_DIR=/cache -e NICE=10 -e IONICE_CLASS=2 \
  -v <staging>:/staging:ro -v <cache-vol>:/cache [-v <repo>:/repo] \
  restic/restic:0.19.1 <args>

probe    : cat config                       # 0 ok / 10 init needed / 12 bad password
init     : init
backup   : backup --json --host once --tag app:<name> /staging/<name>.tar.gz
list     : snapshots --json --host once --tag app:<name>
forget   : forget --json --host once --tag app:<name> \
             --keep-daily 7 --keep-weekly 4 --keep-monthly 6 --retry-lock 10m
prune    : prune --retry-lock 30m           # weekly/monthly, NOT json-parseable
check    : check --json --with-cache --retry-lock 30m [--read-data-subset=10%]
recover  : unlock --remove-all              # only when Once knows no run is live
restore  : restore <id>:/staging --target /restore --json
stream   : dump <id> /staging/<name>.tar.gz
```

### Not verified

- S3 and B2 backends end to end (no credentials). Env var names are from the docs.
- `backup` exit code 3 (needs an unreadable source file).
- Prune/check wall time against a genuinely remote multi-GB repo; the numbers
  above are local SSD and only bound the CPU/structural cost, not the network cost.
- Append-only rest-server behaviour with `unlock`.

### Sources

- restic v0.19.1 source and docs: `doc/075_scripting.rst` (env vars, exit codes,
  JSON output), `doc/045_working_with_repos.rst` (check, `_checking-integrity`),
  `doc/060_forget.rst` (forget/prune, `_customize-pruning`), `doc/cache.rst` and
  `doc/manual_rest.rst` (`_caching`), `internal/repository/lock_file.go` and
  `lock_file_unix.go` (lock staleness), `cmd/restic/cmd_*.go` (per-command lock
  class), `cmd/restic/cmd_unlock.go`, `docker/Dockerfile.release`,
  `docker/entrypoint.sh`, `docker/README.md`.
- Rendered docs: https://restic.readthedocs.io/en/stable/075_scripting.html
- Image: `restic/restic:latest`
  `sha256:9940e6c7421ab3a21ee9fdc4e91596d04c792b600d31b102696c75cbf3ac4481`.
- Docker Hub tag list: `https://hub.docker.com/v2/repositories/restic/restic/tags`.
- Release: `https://api.github.com/repos/restic/restic/releases/latest` -> `v0.19.1`.
