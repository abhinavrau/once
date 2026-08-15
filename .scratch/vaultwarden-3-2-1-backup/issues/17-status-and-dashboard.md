# 17 - Backup status and dashboard surfaces

Status: done

**What to build:** The rendering layer for the four decided facts (issues/06,
07; PRD "Observability"). Wording and layout are decided here, in this issue -
the facts are not renegotiable:

- Offsite staleness ("offsite copy is N days old") from the
  `restic snapshots` query, never from state.
- Last `check` result per repo, alarming separately from run failure
  ("your repo is damaged" is a different emergency from "your backup did not
  run").
- The same-device warning (local repo shares a device with the Docker root),
  persistent, never a refusal.
- Configured-but-not-enabled with its last probe error.

Surfaces: `once backup status` (full rendering) and the TUI dashboard
(compact - pick the minimal set that earns dashboard space; per-app last-run
stays in Settings > Backups). Follow ADR-0012: rendering differs per
frontend, facts do not.

Also here: offsite upload progress through the ADR-0012 progress adapter -
the only step a user waits on. Stream restic's `--json` status lines
(~10/sec) and render transfer progress; take the last `summary` line for the
result, as everywhere else.

**Blocked by:** 15.

- [x] `once backup status` renders all four facts, including a healthy
      install's happy path
- [x] Dashboard shows staleness, check alarm, and the same-device warning;
      pixel-picky review of alignment and truncation at 80 columns
- [x] Offsite leg reports progress live in CLI and TUI per ADR-0012
- [x] Snapshots query failure (offsite unreachable) renders as unknown
      staleness, not a crash and not "never backed up"
- [x] Lint, unit, and integration tests pass

## Comments

### The facts are a type now; the wording is per frontend

`docker.BackupStatus` gathers the four facts once
(`internal/docker/backup_status.go`) and both frontends render it. That is
ADR-0012's shape applied to observability rather than progress: the gather
returns data and queries nothing again, `renderBackupStatus` turns it into
CLI text, `backupNoteLines` picks the dashboard's share. `OffsiteAge` carries
the staleness answer in its three real states - not asked, asked and
unanswerable, answered - so no caller can accidentally collapse the middle
one into "no copy".

**Wording decided here.** `once backup status` is a table:

```
Backups: enabled
  Local repository:   /var/lib/once/backups
  Offsite repository: s3:s3.amazonaws.com/bucket/once

Applications
  vaultwarden  last backup ok 2026-08-14 03:04, offsite copy is 6 hours old
  writebook    not included in backups

Repository checks
  local    ok 2026-08-14 03:04
  offsite  ok 2026-08-14 03:06
```

Every unbounded restic message goes on its own continuation line, aligned to
the value column. Eyeballing the first draft caught the reason for this: a
failed app row printed its error twice - once inlined next to the verdict,
once below - and the inline copy pushed the row past 80 columns. `PROBLEM`
rather than `failed` for a check, because "your repo is damaged" is a
different emergency from "your backup did not run" and sharing one word is
how that distinction gets lost.

### The dashboard's share

Three lines under the panels, in the slot the tailnet warning already uses:
the failed-check alarm (the fact that is otherwise entirely silent), the
same-device warning, and one headline. The headline is a configuration that
never got enabled *or* one staleness answer, never both - a stale age is
misleading when nothing is writing to the repo - so the not-enabled fact
costs no extra row.

`OffsiteSummary` collapses per-app staleness into the one answer a compact
surface has room for, worst-first: a definite gap outranks a repo that could
not be asked, which outranks the oldest copy that exists.

**80 columns, verified by rendering it.** Every row is padded to exactly the
terminal width, centered, and truncated rather than wrapped - the viewport
gives up exactly these rows, and a wrap would silently take one more from the
app panels. `TestBackupNoteFitsEightyColumns` asserts the width of every
emitted row; `TestDashboardBackupNoteShrinksTheViewport` asserts the total
height is unchanged.

### Progress: the seam, and its honest ceiling

The restic container's logs are now **followed while it runs** rather than
collected after it exits (`streamContainerLogs`), so `restic backup --json`'s
status lines reach a renderer while the upload is still happening.
`DeployStageUploading` joins the vocabulary - a measured send, percentage
like a download, plus a Status naming what is going out. The round trips with
no output of their own (`cat config`, `init`, `forget`) announce themselves as
waiting statuses instead, which is what a fresh remote repo actually spends
its minutes on.

This also fixed a latent bug found in review: the restic containers run with
`max-size: 10m, max-file: 1`, so a long `--json` backup could rotate away its
own early output before the old collect-after-exit read it.

**Named ceiling, and it is the honest reading of "the only step a user waits
on".** The callback is wired through the enable path only - `ConfigureBackups`
-> `ValidateBackupRepos` -> `probeRepo`, non-nil from both the CLI and the
Backups overlay. `RunBackups` still passes nil, because ADR-0012 says the
headless daemon has nothing to render and there is no user-facing "back up
now" command to give it a caller; wiring it would be dead code. The
consequence: **today nothing renders a real transfer percentage**, because the
only interactive restic backup uploads a ~30-byte probe file and finishes
before restic emits a status line. The percentage path is covered by unit
tests against real restic JSON. The seam is ready for the first user-waited
restic run that moves real bytes - issue 16's restore is the obvious one.

### Review fixes worth recording

- The dashboard gather had no deadline and refreshed on a fixed interval, so
  an unreachable backend (issues/15: ~15 minutes of restic backoff) would have
  stacked one restic container per app every 5 minutes. It is now bounded by
  `backupStatusTimeout` and arms its next run only when a result comes back:
  one gather in flight, by construction.
- "no offsite copy configured" was decided from the snapshots query rather
  than from `OffsiteURL`, so an install with an offsite repo but no
  participating app read as though none were configured.
- The duration wording lives on `OffsiteAge.AgeText()`, in the facts, so the
  two surfaces cannot disagree about how old the same copy is. Per-frontend
  *rendering* is ADR-0012; duplicated duration arithmetic is just a bug
  waiting to diverge.
- The CLI's progress callback was a blocking channel send. It runs inside
  restic's log demux now, ten times a second during an upload, so a stalled
  render loop would have stalled the work itself. It drops instead, matching
  what the TUI's `progressStream` already did.

### The integration suite outgrew Go's default timeout

`make integration` was failing on a 600s timeout **before this ticket**. The
suite takes ~636s of real work; reverting this branch's test additions (~8s,
measured) leaves ~628s. The 10-minute "budget" was never a decision, only Go's
`go test` default with no `-timeout` flag set.

Rather than delete legitimate coverage to fit an accidental limit, the
Makefile now states `-timeout 20m` with a comment saying it is a hang
detector, not a budget, and the AGENTS.md testing note is updated to match.
**Coordinator: this is the one change here outside issue 17's scope** - it is
one line plus a doc note, and trivially reversible, but issues 16 and 18 are
both adding integration tests to the same suite and would have hit it too.
