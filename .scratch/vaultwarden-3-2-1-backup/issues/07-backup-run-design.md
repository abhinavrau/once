# Design: what one backup run does

Status: done
Assignee: Abhinav Rau
Labels: wayfinder:grilling
Blocked by: 02 (resolved), 03 (resolved), 05 (resolved), 06 (resolved)

## Question

Specify a single scheduled backup run end to end, including every failure branch.

The intended shape: stage the archive to disk, then run restic in a container to
back it up **twice - once to the local repo, once to the remote** - with
`check` and `forget --prune` against each, then delete the staging file.

**Decided (supersedes the charting lock on `restic copy`):** back up twice. The
two repos are independent, so a wedged lock or a full local disk cannot take the
offsite copy down with it. The remote is still `init`ed with
`--copy-chunker-params`, keeping `copy` available as a one-shot backfill when a
remote is enrolled late. Consequences to specify:

- Both backups read the **same staging file**, so it is deleted only after the
  second completes - and the free-disk check happens once, before staging.
- `--host` and the staging path must be **identical** for both repos, or the two
  retention groups diverge (ticket 02's `--group-by host,paths` trap).
- `forget --prune` runs against each repo separately. Same policy, twice.
- The remote backup runs against the network on every backup rather than only
  when copying, so its failure mode is the common case, not the exception - this
  is what the "offsite is N days stale" signal below is really reporting.

Specify:

- **Scheduling.** Where this hooks into the background runner
  (internal/background/runner.go, `AutomaticTaskInterval = 24h`,
  application.go:45). Daily, alongside auto-update - in what order, and what
  happens if a roll is in progress?
- **Staging.** Where the staging file goes, how the location is chosen, and the
  free-disk check before writing it. What happens when disk is short: fail
  loudly, or skip the run? The archive is written by the existing
  `BackupToFile`, so specify what is reused versus new.
- **The restic container.** Pinned image and tag, bind mounts (staging, local
  repo, cache), env for repo and password and backend credentials, network
  access for the offsite leg, and cleanup. Note that Once has five hand-rolled
  `ContainerCreate` call sites and no shared boot helper - decide whether this
  becomes a sixth or whether the two near-identical temp-container helpers in
  application_backup.go (`populateVolume`, `runRestoreHook`) get factored with it.
- **The pause window.** Pause-and-copy is what makes the SQLite copy coherent.
  Confirm the window covers only the volume copy, not the restic upload, and
  state the expected duration for a vault-sized volume.
- **Failure semantics per step.** Locked: offsite copy failure logs but does not
  fail the run. Specify the rest - local backup failure, check failure, prune
  failure, stale restic lock, wrong password, unreachable remote. Which of these
  mark the run failed, which retry, which alert.
- **Constraints ticket 02 established - the run must satisfy all of these.**
  - Pass a **fixed** `--host` (via `RESTIC_HOST`) and a **fixed** staging path
    such as `/staging/<app>.tar.gz`. Docker's random hostname or a date-stamped
    filename would make `forget --group-by host,paths` treat every run as its own
    group, so retention silently never deletes anything. This is a silent
    failure, so specify it as a test, not a comment.
  - Init the repo by probing `restic cat config` first (exit 10 = init, 0 = skip,
    12 = wrong password). Never treat `init`'s exit 1 as "already exists" - it
    also covers permission and backend errors.
  - `check`, `forget` and `prune` all take an **exclusive lock**, so they cannot
    overlap a backup. Sequence them within the run.
  - Handle stale locks explicitly: restic cannot detect them under Docker in
    either hostname configuration, so a killed run wedges the repo until
    `unlock --remove-all`. Once is the single writer, so that is safe - but it
    must be a deliberate recovery step with a log line, never automatic on every
    run.
  - Parse `--json` by streaming and taking the **last** `summary` line (~10
    status lines/sec precede it). `prune` does not support `--json` at all - only
    its `exit_error` is JSON. `check` has no distinct exit code for corruption;
    read `num_errors`, `suggest_repair_index` and `suggest_prune`.
  - Fix the container uid once. A repo created by root is `root:root` 0700 and a
    later `--user 1000:1000` run fails with permission denied - a one-way door.
- **Constraints ticket 03 added.**
  - **Nothing resumes.** Neither `backup` nor `copy` checkpoints before
    `indexMaxAge` (10 min) or 50k blobs, so any run killed under ~10 minutes
    orphans all its work and redoes it. Design retry accordingly, and do not
    promise resumability.
  - A SIGKILLed run leaves locks on **both** repos, and leaves `-tmp-` files that
    neither `prune` nor `check` can see - 32 MB of permanently invisible disk in
    ticket 03's test. Specify how these get reclaimed, or the local repo grows
    silently forever.
  - For the backfill path only: `copy` reads `--from-repo` and writes `-r`, so
    `-r` is the *offsite* repo. Spell the direction out wherever it appears.
    `RESTIC_FROM_PASSWORD` works but is absent from `restic help copy` and does
    **not** fall back to `RESTIC_PASSWORD`.
- **Observability.** `State.RecordBackup` already records per-app results
  (application_backup.go:62). Extend it or add alongside? The staleness signal
  ("offsite copy is 6 days old") and the last `check` result need a home in
  `once status` and the dashboard - specify both renderings.
- **Concurrency.** Two runs must not overlap, and a manual `once backup` must
  not collide with the scheduled one.
- **Do not trust the proxy for liveness.** Ticket 01 established that kamal-proxy
  stops health-checking a single target once it is healthy and never resumes -
  a stopped app still reads as `running` in `kamal-proxy list` while the proxy
  returns 502. If a backup run wants to know whether the app is actually up
  before pausing it, it must check directly, not ask the proxy.

## Resolution

### The run, end to end

A **Backup Run** is install-wide, not per-Application. One run:

1. Take the host flock. If it is held, log and skip - this is not a failure.
2. Reclaim orphaned `-tmp-` files from the local repo (see *Recovery*).
3. For each participating Application whose backup is due, in turn:
   a. Check free disk on the staging device.
   b. Pause the App Container, write the Staging File, unpause.
   c. `restic backup` the Staging File to the **local** Backup Repo.
   d. `restic backup` the same Staging File to the **offsite** Backup Repo.
   e. Delete the Staging File. Record the run and the archive size.
4. Once, per repo: `restic forget --prune`.
5. Once, per repo: `restic check` (metadata only, no `--read-data`).
6. Release the flock.

Steps 4 and 5 run only if step 3 backed up at least one Application. With
nothing due there is no run at all, so `check` and `prune` execute at most daily
rather than on every 5-minute tick.

Both `backup` calls read the **same Staging File**, which is why it is deleted
only after the second one returns, and why the free-disk check happens once,
before it is written.

### Scheduling

`checkBackup` leaves the per-Application loop in `runner.go` and becomes a
sibling of `checkSelfUpdate`:

```go
r.checkSelfUpdate(ctx, ns, state)
r.runBackups(ctx, ns, state)          // install-wide, before the loop
for _, app := range ns.Applications() {
    r.checkUpdate(ctx, app, state)
    r.checkFunnelExpiry(ctx, app)
}
```

Two reasons, one structural and one about ordering.

**Structural:** `backup` is per-Application but `check` and `forget` are
per-repo. Left inside the loop, an install with four Applications takes eight
exclusive repo locks it does not need, and produces a per-Application `check`
result for a thing that is not per-Application. Ticket 02's `--group-by
host,paths` finding is what makes the lifted version correct: a pinned per-app
staging path puts each Application in its own retention group automatically, so
a **single untagged `forget`** applies the fixed policy correctly to every
Application in one lock.

**Ordering:** backups now run *before* auto-update, reversing today's order. When
both fall due on the same tick, this makes the newest snapshot one of the
known-good version. Under the old order the newest snapshot is of whatever the
new image did to the Application - which is the state you are trying to escape.

`BackupDue(appName)` still gates participation per Application, so a run backs up
only what is actually due.

### The retry cadence, fixed in passing

`State.operationDue` (state.go:81) returns due whenever `result.Error != ""`,
with no time condition - the 24h `AutomaticTaskInterval` gates only *successful*
runs. One permanently failing backup therefore retries on every `CheckInterval`
tick: 288 attempts a day, each pausing an App Container, writing a full Staging
File, and calling the offsite backend.

This is pre-existing and it hits Update identically. Today's tarball dump makes
it merely wasteful; against an object store it is a bill and a rate limit. It is
in scope here because the retry cadence *is* part of the run's failure
semantics.

```go
return result.Error != "" && time.Since(result.At) >= backupRetryInterval ||
    time.Since(result.At) >= AutomaticTaskInterval
```

with `backupRetryInterval = 1h`. One condition, in one place, correcting Backup
and Update together.

### Staging and the free-disk check

The **Staging Directory** is `<LocalPath>.staging` - a suffix on the configured
Backup Repo path, not a child of it. `/var/lib/once/backups` gives
`/var/lib/once/backups.staging`.

Same parent, so it inherits the device the user chose for their Backup Repo: a
user who points `LocalPath` at a large external disk gets staging there too,
rather than filling the system disk that Docker also lives on. Not *inside* the
repo, so nothing Once writes can be mistaken for repo content and `init` never
sees a non-empty directory. Derived, so it is not a second setting to configure
or get wrong.

Host `<LocalPath>.staging` bind-mounts to `/staging` in the restic container
**read-only** - Once writes the file, restic only reads it, and the delete is a
host-side `os.Remove`. The in-container path stays `/staging/<app>.tar`, fixed
forever per ticket 05.

`BackupToFile` is reused unchanged to write it; nothing about the archive
changes here beyond ticket 05's drop of gzip.

**Free disk.** Once cannot know the archive size before writing it, and there is
no cheap volume-size query. So it checks `statDisk` (internal/system/disk.go,
already present) on the staging device against **2x the last archive size**,
recorded per Application:

```go
type AppState struct {
    LastBackup       OperationResult `json:"lastBackup"`
    LastUpdate       OperationResult `json:"lastUpdate"`
    LastArchiveBytes int64           `json:"lastArchiveBytes,omitempty"`
}
```

Zero means no baseline - a newly participating Application - and the check is
skipped for that run. **The baseline is per-Application and cannot be
established install-wide**, which is what shapes the enable-time validation
below.

Short disk **fails the run**, loudly: recorded as a failed run, logged, surfaced
in status. Never a silent skip. Filling the system disk takes Docker down with
it, so a quiet skip trades a visible backup failure for an invisible one.

### The restic container

One container per restic command, behind a single narrow helper:

```go
func (n *Namespace) runRestic(ctx context.Context, repo resticRepo, args ...string) (resticResult, error)
```

create → start → `ContainerWait` → collect logs → remove. A run issues roughly
seven of these.

The alternative - one long-lived container plus `execInContainer` - was rejected
on two counts. It buys back the exec-over-hijacked-connection seam ADR-0009
names as unfakeable at the client seam, and it forces one environment across
commands that address two different repos. `ContainerWait` hands back the exit
code directly, which matters because ticket 02's entire init protocol is
exit-code-driven (10 = init, 0 = skip, 12 = wrong password).

Container specification, fixed here so it is decided once:

- **Image** `restic/restic:0.19.1`, pinned by digest. Tag alone would let a
  silent upstream retag change restic's behaviour under a repo Once cannot
  re-create.
- **uid: root (0), permanently.** Ticket 02: a repo created by root is
  `root:root` 0700 and a later `--user 1000:1000` run fails with permission
  denied. A one-way door, so it is chosen once and written down, not left to a
  future refactor.
- **Mounts:** `<LocalPath>.staging` → `/staging` (ro); `<LocalPath>` → `/repo`
  (rw), local leg only; named volume `{namespace}-restic-cache` →
  `/root/.cache/restic` (rw). A named volume rather than a host bind, so the
  cache survives, stays inside Docker's state like everything else Once owns,
  and is not a directory the user has to know about.
- **Network:** the default bridge on every invocation. The offsite leg needs it
  and a per-leg network is a branch that buys nothing.
- **Env:** `RESTIC_REPOSITORY`, `RESTIC_PASSWORD`, and for the offsite leg the
  `OffsiteEnv` map splatted in. The password reaches restic through the
  environment only, never argv - ADR-0013. `RESTIC_COMPRESSION` is left at its
  default `auto` (ticket 05).
- **`--host` is passed as an explicit flag** on every `backup` and `forget`,
  valued `{namespace}`. Ticket 02 verified `RESTIC_HOST` works, but the flag is
  auditable in a log line and this is precisely the trap that fails silently.
  Namespacing by install also means two Once installs can share one offsite repo
  with independent retention groups.

**Not factored with `populateVolume` and `runRestoreHook`.** The ticket asked.
Those two boot the *Application's own* image, keep it alive, and exec into it;
`runRestic` boots a fixed utility image, runs it to completion, and is read for
its exit code. Merging them yields a helper with a liveness mode flag and two
output paths - an abstraction over a coincidence of shape. Once now has a sixth
`ContainerCreate` call site, deliberately.

### Validation when backups are enabled

Enabling backups runs a **probe** first, and `Enabled` is set only if it passes.
This puts every install-wide failure - unreachable backend, wrong credentials,
a `LocalPath` aimed at a stranger's repo, an uncreatable staging directory - in
front of a human at the one moment a human is present, instead of in a daemon
log at 3am.

The probe is not a real Backup Run, and cannot be. Ticket 06 decided that
ticking "include this app in backups" during an install **opens the Backups
overlay inline, mid-install** - so the common path to configuring a Backup Repo
runs *before* the Application is deployed. There is no App Container to pause
and no Application Data Volume to archive. A "back up a real Application to
validate" design has no target on its main path.

So the probe is synthetic, and bounded:

1. Write a few bytes to `<LocalPath>.staging/.once-probe`. This validates that
   the Staging Directory is creatable and writable - a real failure mode that
   would otherwise first appear on the daemon's tick.
2. `cat config` against each configured repo; `init` on exit 10.
3. `backup /staging/.once-probe`, with the same fixed `--host` and a fixed path.
4. `forget <id>` **without `--prune`**, so the probe leaves no snapshot behind.
   The few bytes of pack data are reclaimed by the first scheduled run's
   `forget --prune`.

Every step is O(1). The probe deliberately does **not** run `check` or `prune`:
both are unbounded on a large pre-existing repo - enrolling an offsite leg
against an existing local repo is exactly that case - and would block a TUI form
for minutes. Both are advisory failures under the semantics below, so not
validating them is consistent.

The fixed probe path matters even though the snapshot is forgotten: if the
`forget` itself fails, repeated probes land in **one** retention group rather
than minting a new group per attempt. Ticket 02's trap, applied to the probe.

**It does not solve sizing.** The free-disk baseline is per-Application, so an
Application added next month still has no baseline and still skips its first
check. That is accepted, not worked around.

**On failure, everything is persisted except `Enabled`.** The whole
`BackupRepoSettings` is saved, password included. Two reasons: a typo in a
bucket name must not cost the user every credential name and the password
custody ritual over again; and if the probe reached `init`, a Backup Repo now
exists on disk encrypted with that password, so discarding it would orphan the
repo permanently. Once reports the restic error verbatim, keeps the form
populated, and offers retry. `once backup status` shows configured-but-not-
enabled, with the last error.

**Ordering against the password moment.** The password must exist before `init`,
and the custody ritual should not be demanded for a configuration that then
fails. So: generate the password → run the probe → on success run ticket 11's
custody flow → set `Enabled`. Noted on ticket 11, which owns that flow.

### Failure semantics

| Step | On failure |
|---|---|
| flock not acquired | Skip the tick. Log. **Not** a failure - nothing is recorded. |
| Free-space check | **Fail the run.** |
| Pause / write Staging File | **Fail the run.** |
| Unpause | Existing `ErrUnpauseFailed`. Fails the run, and the Staging File is kept (today's code already declines to delete on this error). The Application is left paused - an emergency, and treated as one. |
| Local `cat config` / `init` / `backup` | **Fail the run.** |
| Offsite `cat config` / `init` / `backup` | Log. The run still **succeeds**. Surfaced by the staleness signal, not by the run result. |
| `forget --prune`, either repo | Log. Run succeeds. The backup is already safe; retention catches up on the next run. |
| `check`, either repo | Log, record the result. Run **succeeds** - the backup did work. Alarms separately: "your repo is damaged" is a different emergency from "your backup did not run", and collapsing them into one failure flag loses that. |
| Delete Staging File | Log. The path is fixed, so the next run overwrites it. |

Because the offsite leg now runs on every backup rather than only when copying,
**its failure is the common case, not the exception** - a flaky uplink produces
it routinely. That is exactly what the staleness signal reports, and it is why
offsite failure must not fail the run: a run marked failed retries hourly and
re-pauses the Application, which is the wrong response to a network that will be
back in twenty minutes.

**Nothing resumes.** Ticket 03 established that neither `backup` nor `copy`
checkpoints before `indexMaxAge` (10 min) or 50k blobs, so a run killed inside
~10 minutes orphans all of its work. Retry is therefore a full redo, the PRD must
not promise resumability, and the hourly retry interval is sized with that in
mind.

**Exit code 12 (wrong password)** cannot arise from Once's own password - only
from a `LocalPath` aimed at a repo Once did not create, which the enable-time
probe now catches. In a scheduled run it fails like any local failure and retries
hourly; a `cat config` probe is cheap, and it self-heals the moment the user
corrects the path.

### Recovery: stale locks and invisible temp files

Ticket 02: restic cannot detect a stale lock under Docker in either hostname
configuration, so a SIGKILLed run wedges the repo until `unlock --remove-all`,
and it asked that this never be automatic on every run. Ticket 03: the same
SIGKILL leaves `-tmp-` files that neither `prune` nor `check` can see - 32 MB in
its test - so the repo grows invisibly forever.

**Locks: evidence-triggered, never unconditional.** The flock makes Once
provably the only writer *on this host*, which is tempting - but an offsite repo
may be shared with another host, where an unconditional unlock would delete a
live lock belonging to someone else. So: when a restic command fails **with a
lock error**, run `unlock --remove-all` against that repo, log it at warn level,
and retry the command once. It never fires on a healthy run, it needs no user
action, and it is deliberate in the sense ticket 02 asked for - triggered by
evidence rather than scheduled.

**Temp files: local only.** After taking the flock, glob
`<LocalPath>/data/**/*-tmp-*` and delete anything older than 24h. Once has no
filesystem reach into the offsite repo and restic cannot see these files
through the API, so **the offsite leak is accepted** - a bounded amount of dead
space per SIGKILL, recorded here as a known ceiling rather than silently
tolerated.

### Observability

`AppState.LastBackup` stays a **single** `OperationResult` and means **the local
leg**. It is what drives `BackupDue`, and the local leg is what determines
whether a run must be retried. The offsite leg gets no state field.

That is not an oversight. Ticket 06 established that the state file lives in the
Proxy container's writable layer, so a Proxy roll - a port change, an image
upgrade - erases it. A state-backed offsite record would therefore announce
"never backed up" to a user whose repo is full of snapshots. So the user-facing
staleness signal is a **query**: `restic snapshots --latest 1 --tag app:<name>`
against the offsite repo, which no Proxy roll can erase.

The `check` result joins `State` as an install-wide field keyed by repo, per
ticket 06 - `check` covers a repo, not an Application:

```go
type State struct {
    Apps           map[string]*AppState       `json:"apps"`
    LastSelfUpdate OperationResult            `json:"lastSelfUpdate"`
    LastCheck      map[string]OperationResult `json:"lastCheck,omitempty"` // "local" | "offsite"
}
```

`check` has no distinct exit code for corruption (ticket 02), so the recorded
result is derived from `--json`: `num_errors`, `suggest_repair_index`,
`suggest_prune`. `--json` emits ~10 status lines/sec, so every parse streams and
takes the **last** `summary` line. `prune` ignores `--json` entirely - only its
`exit_error` is JSON - so it is read by exit code alone.

Four facts now exist for `once backup status` and the dashboard to render: the
offsite staleness signal, the last `check` result per repo, the same-device
warning from ticket 06, and configured-but-not-enabled with its last error.
Their wording and layout remain unspecified and stay on the map.

### Concurrency

A host flock at `/var/lib/once/backup.lock`, taken non-blocking (`syscall.Flock`,
`LOCK_EX|LOCK_NB`) by the daemon's run and by manual `once backup` alike.

Two scheduled runs cannot overlap - the daemon is a single goroutine. The real
collision is cross-process: a manual `once backup <host> <file>` racing the
daemon. Both call `ContainerPause` on the same App Container; `ContainerPause`
on an already-paused container errors, and the first process's unpause `defer`
resumes the Application out from under the second one's `CopyFromContainer` -
producing exactly the torn `db.sqlite3` / `db.sqlite3-wal` pair the pause exists
to prevent. Silently, in a file that looks like a backup.

restic's own repo lock covers the restic half and nothing of the pause-and-stage
half, so it cannot serve here. There is no lock primitive anywhere in Once today
- this is the first.

Daemon cannot take it → log and skip the tick. CLI cannot take it → tell the user
a backup is in progress and exit non-zero.

### The pause window

Confirmed, and it is the whole reason the Staging File exists: `copyVolumeData`
pauses, streams the data directory into the tar, and unpauses on `defer`. restic
does not start until the Staging File is closed. Nothing about the offsite
upload, the network, `prune` or `check` is inside the pause.

The window is one `CopyFromContainer` of the data directory to local disk.
Ticket 05's measurement against a real Vaultwarden used a 20 MB archive, so for
a vault it is well under a second.

The PRD states it as a **property, not a number**: the pause is proportional to
Application Data size alone - not to repo size, snapshot count, retention
history, or uplink speed - with the vault figure as the worked example. No
duration is promised for arbitrary Applications.

**Do not ask the Proxy whether the Application is up first.** Ticket 01:
kamal-proxy stops health-checking a single target once it is healthy and never
resumes, so a stopped Application still reads `running` in `kamal-proxy list`.
The run already does the right thing today - `copyVolumeData` calls
`ContainerInspect` and reads `State.Running` directly - and that must not be
"optimised" into a proxy query.

### Tests the design requires

These cover the failures that are silent, where a passing install looks
identical to a broken one:

1. **Retention grouping.** Two runs of the same Application produce two
   snapshots in **one** `--group-by host,paths` group. This is ticket 02's trap:
   get it wrong and retention keeps everything forever while every command
   reports success.
2. **`--host` and staging path are constant.** A unit test on the argument
   builder asserting neither varies with time, container, or run.
3. **Free-space check fires** when the staging device lacks 2x the baseline, and
   **is skipped** when the baseline is zero.
4. **The probe leaves no snapshot** - `restic snapshots` is empty after a
   successful enable.
5. **A failed run does not retry inside an hour**, and a successful one does not
   retry inside 24h.
6. **The flock is honoured**: a manual export during a scheduled run exits
   non-zero rather than pausing an already-paused App Container.

The acceptance test already on the map (deploy real `vaultwarden/server`, write
data, back up, restore into a clean namespace) stands, with one amendment from
this ticket: the second repo is populated by a **second `backup`**, not by
`restic copy`.

### Consequences for other tickets

- **Ticket 06** gains an enable-time probe between "configure" and "enabled".
  Noted there.
- **Ticket 11** now has a fixed position in the enable sequence: password
  generated → probe → custody flow → `Enabled`. Noted there.
- **Ticket 08** (restore) inherits `runRestic`, the root uid, the digest-pinned
  image, and the flock.
- **Ticket 09** (the PRD) must carry the `operationDue` retry fix, which is a
  correction to existing Update behaviour and belongs in the release notes.
