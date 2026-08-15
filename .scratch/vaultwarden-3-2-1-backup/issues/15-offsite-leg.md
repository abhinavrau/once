# 15 - The offsite leg: back up twice, surface staleness

Status: done

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

- [x] With an offsite repo configured, each run produces one snapshot in
      each repo from the same Staging File; retention groups stay separate
      per repo and stable per app
- [x] Offsite failure logs, run still recorded successful, local snapshot
      intact; staleness reflects the last offsite snapshot, not state
- [x] `forget --prune` and `check` run per repo; check failure alarms
      separately from run failure
- [x] Offsite init uses `--copy-chunker-params`
- [x] Integration coverage uses a second local directory as the offsite
      repo; no cloud credentials in the suite
- [x] Lint, unit, and integration tests pass

## Comments

Implemented on branch issue-15-offsite. Evidence per checklist item, all from
`TestBackupRunOffsiteLeg` (integration/backup_run_test.go) unless noted.

- **Both legs, one Staging File.** `backupApp` runs `backupToRepo` against
  the local repo and then the offsite one with the same `resticBackupArgs` -
  same `--host`, same `/staging/<app>.tar` - and only then deletes the
  Staging File. The test asserts both repos hold one group with an identical
  `group_key` after run one, two snapshots each after run two, and that the
  Staging File is gone.
- **Offsite failure logs, run succeeds.** Third phase points `OffsiteURL` at
  a repo initialized under another password: `cat config` exits 12
  immediately, the leg logs, `RunBackups` returns nil, `LastBackup` records
  no error, the local snapshot is fresh, and nothing lands in the foreign
  repo.
- **Staleness is a query.** `Namespace.LatestOffsiteSnapshot` runs
  `snapshots --json --no-lock --latest 1 --host <ns> --tag app:<name>` (unit
  test `TestResticLatestSnapshotArgs`; parser `TestParseLatestSnapshotTime`).
  The test erases State entirely and the answer is unchanged. `once backup
  status` renders it per app ("offsite copy is N days old" /
  "no offsite copy yet" / the error) - `TestDescribeOffsiteAge`. Not
  `runResticRetryLock`: a question must never remove another host's lock.
- **Per repo, advisory.** `configuredResticRepos` drives one
  `forget --prune` and one `check` per repo through `maintainRepo`; the
  check result lands in `State.LastCheck[local|offsite]` and status prints
  both. The failure phase asserts `LastCheck["offsite"].Error` set while the
  run and the local check stay clean.
- **`--copy-chunker-params`.** Already on `offsiteResticRepo.initArgs` from
  13; now verified rather than assumed - the test compares
  `restic cat config`'s `chunker_polynomial` across the two repos.
- **A filesystem path is a valid offsite repository** (new, and what makes
  the suite's second-directory coverage possible at all): restic reads a
  bare path as its local backend, so Once bind-mounts it at `/offsite`.
  Left unmounted it would init inside the restic container and vanish with
  it, reporting success the whole way - so `ValidateOffsiteURL` refuses
  anything neither absolute nor scheme-prefixed, at the engine seam and on
  both frontends (`TestValidateOffsiteURL`,
  `TestBackupsFormRejectsUnmountableOffsiteURL`).

### The real S3 round-trip (the item research could not do)

No cloud account here, so the check ran against **MinIO** (`minio/minio`,
S3 API on the docker bridge) - a real S3 protocol endpoint over the network,
driven through Once's own user-facing path:

```
AWS_ACCESS_KEY_ID=… AWS_SECRET_ACCESS_KEY=… \
once -n <ns> backup enable --local-path /tmp/… \
  --offsite-url s3:http://<ip>:9000/once-offsite \
  --offsite-env AWS_ACCESS_KEY_ID,AWS_SECRET_ACCESS_KEY
```

**Result: passthrough works.** The names were captured from Once's
environment, splatted into the restic container, and signed real S3
requests; the probe ran `cat config` (exit 10) -> `init
--copy-chunker-params` -> `backup` -> `forget` against the bucket. The S3
repo's `chunker_polynomial` came out identical to the local repo's
(`2ea894c48c6b69`), so `--copy-chunker-params` travels through
`RESTIC_FROM_REPOSITORY` across a network backend, and the bucket held
`config`/`keys`/`index`/`data` with **no** snapshot left behind.

**Defect found and fixed on the way.** With the bucket *not* yet created,
`restic cat config` does **not** return exit 10. It retries `Stat: The
specified bucket does not exist` with exponential backoff for ~15 minutes
and then fails - so the "bounded, O(1), fits inside a TUI form" probe would
have held the form open that long on a fresh bucket or a typo'd bucket name,
and `ensureRepo` never reaches `init`. `ValidateBackupRepos` now runs under
`backupProbeTimeout` (2m) and, on expiry, says which repo went quiet and to
create the bucket if it does not exist. **Named ceiling:** Once still cannot
create an S3 bucket - the user must, and restic's own bucket-creating `init`
is unreachable behind the `cat config` protocol.

Not verified: Backblaze B2 (`B2_ACCOUNT_ID`/`B2_ACCOUNT_KEY`) - same
`OffsiteEnv` splat, no endpoint to test against; and real AWS S3 as opposed
to the S3 protocol. The scheduled run's offsite leg is *not* time-bounded
(no human waits on it, and a cap could cut a legitimately slow upload), so a
missing bucket costs a daemon run ~15 minutes of backoff before it logs.
