# 15 - The offsite leg: back up twice, surface staleness

Status: ready-for-agent

**What to build:** The Backup Run's second leg (issues/03, 07; PRD "The
Backup Run"). A non-empty `OffsiteURL` turns it on.

- After the local `backup` of each Staging File, run `restic backup` of the
  **same file** against the offsite repo (same `--host`, same in-container
  path - one retention group per app per repo), with `OffsiteEnv` splatted
  into the container environment. The Staging File is deleted only after the
  offsite leg returns.
- Offsite `init` (via the `cat config` probe) passes `--copy-chunker-params`
  so `restic copy` remains available as a one-shot backfill when a remote is
  enrolled late.
- Failure semantics: offsite `cat config`/`init`/`backup` failures log; the
  run **succeeds**. `forget --prune` and `check` now run once per repo, both
  advisory. Evidence-triggered unlock covers the offsite repo too; the
  offsite `-tmp-` leak stays an accepted named ceiling.
- Staleness signal: `restic snapshots --latest 1 --tag app:<name>` against
  the offsite repo (a query, never state - the state file dies with a Proxy
  roll). Exposed to the status/dashboard work in 17; `once backup status`
  gains the raw fact now ("offsite copy is N days old").
- First real S3/B2 round-trip check happens here (research lacked
  credentials): verify `AWS_*`/`B2_*` env passthrough against one real
  backend once, manually, and record the result on this issue.

**Blocked by:** 13 (offsite settings + probe exist there).

- [ ] With an offsite repo configured, each run produces one snapshot in
      each repo from the same Staging File; retention groups stay separate
      per repo and stable per app
- [ ] Offsite failure logs, run still recorded successful, local snapshot
      intact; staleness reflects the last offsite snapshot, not state
- [ ] `forget --prune` and `check` run per repo; check failure alarms
      separately from run failure
- [ ] Offsite init uses `--copy-chunker-params`
- [ ] Integration coverage uses a second local directory as the offsite
      repo; no cloud credentials in the suite
- [ ] Lint, unit, and integration tests pass
