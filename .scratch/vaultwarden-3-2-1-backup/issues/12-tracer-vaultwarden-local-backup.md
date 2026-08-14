# 12 - Tracer: Vaultwarden deploys and backs up to a local repo

Status: ready-for-agent

**What to build:** The tracer bullet - deliberately wide and thin. After this
issue, `once deploy vaultwarden` comes up healthy and a scheduled Backup Run
snapshots it into a local restic repo. No offsite, no TUI, no probe, no
custody flow, no restore-from-repo - those land in 13-16. Cuts are named
below so later issues replace them instead of discovering them.

App Profile (full detail: issues/04, PRD "App Profile"):

- `AppProfile` struct in `internal/docker` with resolved accessors; `Profile`
  field on `catalog.App` and `ApplicationSettings`; constants renamed to
  `Default*` and no longer read directly. Zero value = today's behaviour, no
  backfill.
- All call sites from issues/04: `verifyHTTP`, `volumeMounts`, backup copy
  source, `--health-check-path` through `DeployOptions`/`deployArgs` (always
  passed), `BuildEnv` URL parameter (tailnet URL under Tailnet exposure,
  Deploy warning when a declared `URLEnvVar` resolves empty), naming via
  `UniqueName(Profile.AppName(...))` in `Deploy` and `Restore`, Host
  derivation, `Resolve` returns `catalog.App`, `InstallAppSelectedMsg`
  carries the entry, scraper filters the Proxy's source address instead of
  the path, `Validate` + `Equal` + the three `settingsFlags`
  (`--data-path --health-check-path --url-env-var`, on deploy and update).
- Every catalog entry gains `BaseName` from its `Alias`; Vaultwarden entry
  exactly as in the PRD.
- Not changed: `background/admin.go`'s `/up`; `populateVolume`'s `/data`
  (gets its coupling comment).

Archive format (issues/05):

- Drop gzip from `backupToWriter`; manual export writes plain tar too.
- `parseBackup` sniffs the gzip magic bytes; test restores one gzipped and
  one plain archive.

Local backup engine (issues/06, 07, PRD "The Backup Run"):

- `BackupRepoSettings` as a top-level `backups` key on the proxy label
  (`ProxySettings` field). `AutoBackup` survives as the participation flag;
  `BackupSettings.Path` deleted; `TrimBackups` and scheduled tarball dumps
  retire.
- `runRestic` helper: one container per command, `restic/restic:0.19.1`
  pinned by digest, root uid, staging ro-mount, repo rw-mount, named cache
  volume, `--host {namespace}` explicit, secrets by env only, exit-code
  driven `cat config` init protocol, last-`summary`-line JSON parsing.
- The install-wide Backup Run before the update loop: flock
  (`/var/lib/once/backup.lock`, first lock primitive - manual export takes
  it too), local `-tmp-` reclaim (>24h), per due app free-disk check (2x
  `LastArchiveBytes`, skip at zero baseline, short disk fails loudly), pause
  + stage to `<LocalPath>.staging/<app>.tar` + unpause, local `backup` leg,
  delete staging, then one `forget --prune` (fixed policy) and one `check`
  (no `--read-data`) for the local repo, per the PRD's failure-semantics
  table. Evidence-triggered `unlock --remove-all` + single retry on lock
  errors.
- `operationDue` retry fix: 1h floor on failures, 24h on successes - fixes
  Update too.
- `State` gains `LastCheck` map and `AppState.LastArchiveBytes`.
- Interim enable (replaced in 13): `once backup enable --local-path <p>`
  generates the 32-char base32 password, prints it via the no-TTY banner
  path (which stays forever as the fallback), persists settings, sets
  `Enabled`. `once backup disable` and a minimal `once backup status`.

**Blocked by:** nothing.

- [ ] `once deploy vaultwarden` (catalog) deploys healthy: named
      `vaultwarden`, volume at `/data` only, proxied health check on
      `/alive`, `DOMAIN` set; a custom image with the three flags works; an
      existing Rails app deploys exactly as before
- [ ] Old labels (no `profile` key) decode to zero Profile; label round-trip
      preserves the Profile through backup archive metadata
- [ ] Restore reads both gzipped and plain archives
- [ ] A scheduled run snapshots a due app into the local repo; two runs land
      in one `--group-by host,paths` retention group; `--host` and staging
      path constant (unit test on the argument builder)
- [ ] Free-disk check fires below 2x baseline, skipped at zero baseline
- [ ] Failed run retries after 1h not 5min; success after 24h; manual export
      during a run exits non-zero on the flock
- [ ] Backups run before auto-update in the tick
- [ ] Lint, unit, and integration tests pass
