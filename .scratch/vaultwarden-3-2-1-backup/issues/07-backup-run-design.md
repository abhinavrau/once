# Design: what one backup run does

Status: open
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

(pending)
