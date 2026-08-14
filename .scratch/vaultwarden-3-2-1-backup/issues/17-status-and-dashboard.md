# 17 - Backup status and dashboard surfaces

Status: ready-for-agent

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

- [ ] `once backup status` renders all four facts, including a healthy
      install's happy path
- [ ] Dashboard shows staleness, check alarm, and the same-device warning;
      pixel-picky review of alignment and truncation at 80 columns
- [ ] Offsite leg reports progress live in CLI and TUI per ADR-0012
- [ ] Snapshots query failure (offsite unreachable) renders as unknown
      staleness, not a crash and not "never backed up"
- [ ] Lint, unit, and integration tests pass
