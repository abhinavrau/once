# PRD: Vaultwarden on Once, with 3-2-1 restic backups

Status: ready-for-agent

Decided by the wayfinder map in this directory (MAP.md). Design detail and
evidence live in the ticket resolutions: App Profile (issues/04), archive
format (issues/05), configuration and password custody (issues/06, 11), the
Backup Run (issues/07), restore (issues/08). Research citations: kamal-proxy
health checks (issues/01), restic CLI behaviour (issues/02), the two-repo
topology (issues/03), terminal QR handoff (issues/10).

Produced alongside this spec, already committed: glossary additions to
CONTEXT.md (App Profile, Backup Repo, Offsite Copy, Backup Run, Staging File,
Repo Password, Recovery Kit, and a rewritten Backup / Restore entry),
ADR-0014 (install-wide settings on the proxy label), and an amendment to
ADR-0013's at-rest inventory. One further ADR-0013 amendment line lands with
implementation (see issue 13).

## Problem Statement

Two problems, one map.

**Once can only deploy Rails-shaped apps.** `once deploy vaultwarden/server`
today hard-fails and destroys the volume: kamal-proxy probes `/up` (Vaultwarden
answers on `/alive`), `verifyHTTP` demands 2xx from `/up` and calls
`Remove(ctx, true)` otherwise - taking the Application Data Volume with it.
The volume mounts at `/storage` + `/rails/storage` while Vaultwarden writes
`/data`, so even a lucky deploy would back up an empty directory. And
`NameFromImageRef` names the app `server`.

**Backup today is not a backup strategy.** One `.tar.gz` per app in one local
directory: one copy, same disk as the data, no offsite, no encryption,
retention by filename mtime. When the host dies, everything dies with it. For
a password manager - the highest-stakes data a self-hoster owns - that is
disqualifying.

## Solution

**App Profile** - a four-field struct (`BaseName`, `DataPath`, `HealthPath`,
`URLEnvVar`) on `catalog.App`, copied into `ApplicationSettings` at deploy so
it persists in the `once` label. The zero value means today's behaviour, so no
existing app changes and no backfill exists. Vaultwarden is the worked
example: catalog entry with `/data`, `/alive`, `DOMAIN`, name `vaultwarden`.
Settable by flag for arbitrary images.

**3-2-1 backups via restic.** The existing archive stream (now plain tar, not
gzip) is staged to disk during a sub-second pause window, then backed up by a
containerized restic **twice - once to a local repo, once to an offsite repo**
(any restic URL + env credentials). Two independent repos, no `restic copy`.
One repo per install, snapshots tagged `app:<name>`, fixed retention
(`--keep-daily 7 --keep-weekly 4 --keep-monthly 6 --prune`). The repo password
is user-held: generated at configure time, validated by a probe, handed over
once via a custody flow (on screen, or QR to the user's phone), typed back to
prove custody, cached in the proxy label so unattended runs work. Lose it,
lose the backups, by design.

**Restore grows repo awareness.** `once restore <app> --repo <url>` pulls the
snapshot into staging and hands it to the existing `ns.Restore`. Bare-metal
recovery is the headline: a fresh host, the repo URL, and the password are
sufficient to list the repo, restore the app, and resume backups.

## User Stories

1. As a self-hoster, I want `once deploy vaultwarden` (catalog) to come up
   healthy with its data on the volume at `/data`, probed at `/alive`, with
   `DOMAIN` set, named `vaultwarden` - so that a non-Rails app just works.
2. As a self-hoster deploying an arbitrary image, I want `--data-path`,
   `--health-check-path`, and `--url-env-var` flags, so that a failed health
   probe cannot silently destroy my data volume.
3. As a self-hoster with existing apps, I want deploys and labels from before
   App Profiles to behave exactly as today, with no migration.
4. As a TUI user installing a custom image, I want to be asked the health
   check path unconditionally, so that `verifyHTTP`'s destructive default
   never applies to an app that answers elsewhere.
5. As a self-hoster, I want `once backup enable --local-path <p>
   --offsite-url <u> --offsite-env <names>` to validate both repos with a
   bounded probe while I am present, so that misconfiguration surfaces now,
   not in a daemon log at 3am.
6. As a self-hoster, I want the repo password shown exactly once - on screen
   or sent to my phone as a QR of a one-time URL - with a Recovery Kit
   (password, offsite URL, credential names, local path) and a type-it-back
   gate, so that I provably hold what recovery depends on.
7. As a self-hoster scripting installs, I want a no-TTY `once backup enable`
   to print the password with a banner and not hang on a prompt.
8. As a self-hoster, I want per-app participation ("Include this app in
   backups") in the install flow and per-app settings, with the install-wide
   repo form opening inline the first time, so that nomination is explicit.
9. As a self-hoster whose local repo shares a disk with Docker, I want a
   persistent warning, not a refusal.
10. As a self-hoster, I want daily unattended Backup Runs that pause each app
    only for the volume copy, snapshot to both repos, then prune and check -
    so that backups cost no downtime.
11. As a self-hoster with a flaky uplink, I want an offsite failure to log
    without failing the run, and "offsite copy is N days old" surfaced in
    status and the dashboard.
12. As a self-hoster, I want a failed backup or update to retry hourly, not
    every 5 minutes, so that a broken backend is not 288 paused-container
    attempts a day.
13. As a self-hoster, I want `once backup <host> <file>` manual export
    unchanged in shape, and refused with a clear message while a scheduled
    run holds the lock, so that racing copies cannot tear a SQLite pair.
14. As a self-hoster whose host is gone, I want `once restore --repo <url>`
    on a fresh install to list the repo's apps, `once restore vaultwarden
    --repo <url>` to restore the newest snapshot, and a "Resume backups to
    this repo?" prompt after - so that recovery is three commands.
15. As a self-hoster, I want `--list` and `--snapshot <id>` to pick an older
    snapshot, with the pick printed before restore.
16. As a self-hoster on a configured install, I want bare `once restore
    <app>` to read the local repo and never silently fall back to offsite.
17. As a self-hoster with pre-restic `.tar.gz` archives, I want `once restore
    <file>` to keep restoring them forever.
18. As a self-hoster restoring an app that already exists, I want a refusal
    naming the colliding app and the `once app destroy` command.
19. As a Once developer, I want tests for the failures that report success
    (retention grouping, fixed `--host`/staging path, probe leaves no
    snapshot), so that a broken install cannot look identical to a working
    one.
20. As a Once developer, I want the whole suite to run with no cloud
    credentials - local repos only, offsite stood in by a second directory.

## Implementation Decisions

### App Profile (issues/04)

- `AppProfile` lives in `internal/docker`: four `omitempty` string fields
  (`BaseName`, `DataPath`, `HealthPath`, `URLEnvVar`), comparable, nested as
  `Profile` on both `catalog.App` and `ApplicationSettings`. Resolved
  accessors (`AppName(imageRef)`, `MountTargets()`, `HealthCheckPath()`)
  interpret the zero value as Once's built-in behaviour; the package constants
  become `DefaultHealthCheckPath` / `DefaultAppVolumeMountTargets` and stop
  being read directly.
- A custom `DataPath` **replaces** both Rails mount targets; it never adds a
  third mount.
- `DefaultApplicationSettings` stays the single source of defaults; layering
  at deploy is defaults -> catalog Profile -> flags. Old labels decode to the
  zero Profile and behave exactly as today - **no backfill**.
- `catalog.Resolve` returns `catalog.App` (unmatched -> `App{ImageRef: s}`);
  `InstallAppSelectedMsg` carries the `catalog.App`, not an image-ref string.
- `Deploy` and `Restore` name apps via
  `UniqueName(settings.Profile.AppName(settings.Image))`; Host derivation uses
  the same accessor. Every catalog entry sets `BaseName` from its `Alias`
  (fixes Campfire deploying as `once-campfire`; new deploys only).
- kamal-proxy gets `--health-check-path` on every deploy (its own default is
  `/up`, so passing it is a no-op for Rails apps). `verifyHTTP`, `volumeMounts`
  and the backup copy source read the Profile.
- The URL env var value is **computed, never stored**: `BuildEnv` appends
  `URLEnvVar=<url>` before the user env loop (explicit `--env` wins). Tailnet
  exposure uses `TailscaleSettings.TailnetURL(host)`; Deploy warns when a
  declared `URLEnvVar` resolves empty.
- User stats stop filtering by path: the scraper filters the Proxy's source
  address instead, covering every health path permanently.
- Validation in `ApplicationSettings.Validate`: `DataPath` absolute and not
  `/`; `HealthPath` starts with `/`; `URLEnvVar` contains no `=`.
- Restore needs no mount change: `populateVolume`'s `/data` pairs with the
  archive layout (`BackupDataDir`), not the app's data path. It keeps its
  literal with a comment. `background/admin.go`'s `/up` is Once's own server
  and stays a literal.
- Vaultwarden catalog entry: `Name: "Vaultwarden"`, `Alias: "vaultwarden"`,
  `ImageRef: "vaultwarden/server"` (no tag, like the other entries),
  `Profile: {BaseName: vaultwarden, DataPath: /data, HealthPath: /alive,
  URLEnvVar: DOMAIN}`. `AutoUpdate` stays on.

### Archive format (issues/05)

- **gzip is dropped**; the archive Once produces is plain `.tar`, in both the
  restic path and the manual export - one format, one code path. Rationale:
  gzip defeats restic's chunker (19.085 MiB re-added daily vs 981 KiB plain).
- `parseBackup` sniffs the gzip magic bytes (`0x1f 0x8b`) and decompresses
  only when present, so every existing `.tar.gz` archive stays restorable.
  Non-optional; covered by a test restoring one gzipped and one plain archive.
- Staging path fixed **forever** at `/staging/<app>.tar` (retention grouping,
  issues/02). `RESTIC_COMPRESSION` stays `auto` - restic compresses at chunk
  granularity.

### Backup Repo settings and configuration (issues/06)

- `BackupRepoSettings{Enabled, LocalPath, OffsiteURL, OffsiteEnv
  map[string]string, Password}` is a field on `ProxySettings` - a **top-level
  key** in the proxy label JSON, which is what lets an older binary's rewrite
  carry it through `labelExtra` instead of destroying the password (ADR-0014).
- `Enabled` is explicit; a non-empty `OffsiteURL` is the offsite switch.
- **No observed state on any label** (labels are immutable; a `lastRun` would
  recreate the Proxy per run). The `check` result joins the state file inside
  the Proxy container; user-facing staleness is a `restic snapshots --latest 1
  --tag app:<name>` query, because the state file is a cache a Proxy roll
  erases.
- Local repo defaults to `/var/lib/once/backups`; when it shares a device
  with the Docker root, warn persistently in status and dashboard - never
  refuse.
- Backend credentials: CLI takes the **names**
  (`--offsite-env AWS_ACCESS_KEY_ID,AWS_SECRET_ACCESS_KEY`) and reads values
  from Once's environment (ADR-0013); stored plaintext in `OffsiteEnv`,
  splatted into the restic container env. The S3/B2 paths were not verified
  end to end for lack of credentials - first real cloud round-trip is an
  implementation-time check.
- CLI: `once backup <host> <file>` (export, unchanged) plus `enable`,
  `disable`, `status` subcommands. Named ceiling: an app whose Host is
  literally a subcommand name cannot be exported.
- TUI: install-wide settings in a dashboard-global Backups overlay (the
  `TailscaleForm` shape); per-app Settings > Backups keeps only the "Include
  this app in backups" checkbox (`AutoBackup` survives as the participation
  flag; the path field goes); the install flow asks the same checkbox, and
  ticking it with no repo configured opens the overlay **inline, once**.
- For a custom image, ticking the checkbox forces `DataPath`; the health path
  is asked **unconditionally** (verifyHTTP destroys the volume whether or not
  backups are on).
- Clean break for old settings: `BackupSettings.Path` deleted, `TrimBackups`
  and scheduled tarball dumps retire, no `removedSettingsFields` entry needed
  (nested key), existing `.tar.gz` files left untouched.

### Password custody (issues/06, 11, 10)

- Password: 32 chars base32 (A-Z 2-7, 160 bits, `crypto/rand`), displayed in
  groups of 4. Generated at **configure** time (a human is present by
  construction), before the probe.
- Enable sequence: **generate password -> validation probe -> custody flow ->
  `Enabled = true`**. The probe needs the password (it `init`s the repos);
  the custody ritual is never demanded for a configuration that then fails.
- **One custody path for both frontends** (bubbletea program, run
  sequentially the way `cli_progress.go` already runs progress programs). It
  asks how to take custody: **show on screen**, or **send to my phone** (QR).
  `--password-delivery=screen|qr` selects without asking; no TTY falls back
  to print-with-banner, no gate.
- Reveal screen: prototype variant D - numbered copy / store / prove
  checklist with the bordered ONCE RECOVERY KIT block (password, offsite
  repo URL, credential names, local repo path, created) under step 1,
  trimmed ~3 rows to fit 80x24. ctrl+p copies the password, ctrl+k the whole
  kit (OSC52). **Mouse tracking off on any screen showing a secret.**
- Gate: unmasked type-back, input normalized (case, dashes, spaces),
  mismatch names the stakes, esc opens a loud skip confirm - password stays
  persisted, `Enabled` stays false, finish later via `once backup enable`.
  The clipboard round-trip loophole is accepted: the gate is a nudge.
- QR path: the QR encodes a **one-time URL**, never the secret (no password
  manager ingests raw-text QR). Tailscale on: `TailnetURL("once-admin")`.
  Otherwise: self-signed cert with the LAN IP in the SAN (stdlib),
  `CopyToContainer` into the Proxy, temporary kamal-proxy service, removed on
  screen close. LAN IP = the default route's source address (UDP-dial trick).
  An intro screen precedes the code: same-LAN requirement, single-use +
  10-minute TTL, the cert warning explained, and the Safari
  blank-page-needs-refresh quirk. Half-block rendering via `piglig/go-qr`
  (MIT, +1 module); force colours, guard ambiguous-width locales, suppress
  the QR under a degraded color profile.
- Phone page: username+password form (autocomplete new-password) so the save
  prompt fires; POST-redirect-GET back to itself; copy buttons for password
  and recovery details; explicit Done burns the token; TTL and screen-close
  teardown are backstops. Token check uses `subtle.ConstantTimeCompare`.
  Never serve over plain HTTP (not a secure context; save prompt suppressed).
- No nag for unconfirmed custody: the only mutable store is wiped by Proxy
  rolls, so the nag would false-alarm forever.

### The Backup Run (issues/07)

- A Backup Run is **install-wide**: `runBackups` becomes a sibling of
  `checkSelfUpdate`, running **before** the per-app update loop so the newest
  snapshot is of the known-good version. Per run: flock -> reclaim local
  `-tmp-` orphans (>24h) -> per due app (free-disk check, pause + write
  Staging File + unpause, `restic backup` to local, `restic backup` same file
  to offsite, delete Staging File, record) -> one `forget --prune` per repo
  -> one `check` (no `--read-data`) per repo. Steps after the loop run only
  if something was backed up.
- Staging Directory: `<LocalPath>.staging` (suffix, never a child of the
  repo), bind-mounted read-only at `/staging`. `BackupToFile` reused
  unchanged. Free-disk check: staging device vs **2x the last archive size**
  (`AppState.LastArchiveBytes`); zero baseline skips the check; short disk
  **fails the run loudly**.
- The pause window covers only the volume copy - a property, not a number:
  proportional to Application Data size alone (sub-second for a vault),
  never to repo size or uplink. Liveness is read via `ContainerInspect`,
  never from kamal-proxy (it stops probing a healthy target forever).
- restic container: one per command via
  `runRestic(ctx, repo, args...) (resticResult, error)` - create, start,
  `ContainerWait`, logs, remove. Image `restic/restic:0.19.1` **pinned by
  digest**; **root uid forever** (one-way door); named cache volume
  `{namespace}-restic-cache`; default bridge network; env
  `RESTIC_REPOSITORY`, `RESTIC_PASSWORD`, offsite env splat - secrets never
  argv (ADR-0013). `--host {namespace}` passed as an explicit flag on every
  `backup`/`forget`. Deliberately a sixth `ContainerCreate` site, not
  factored with `populateVolume`/`runRestoreHook`.
- Init protocol: probe `cat config` first (exit 10 = `init`, 0 = skip, 12 =
  wrong password); never treat `init` exit 1 as "already exists". Offsite
  `init` passes `--copy-chunker-params` so `copy` remains a one-shot backfill.
- JSON parsing: stream and take the **last** `summary` line; `check` read via
  `num_errors`/`suggest_repair_index`/`suggest_prune`; `prune` by exit code
  only.
- Retry cadence fix (pre-existing defect, hits Update too): `operationDue`
  gains a 1h floor for failed results (`backupRetryInterval`), 24h for
  successes. Release-notes item.
- Enable-time validation probe (bounded, synthetic - the inline overlay runs
  before any app exists): write `<LocalPath>.staging/.once-probe`, `cat
  config`/`init` per repo, `backup` it with the real `--host` and a **fixed
  path**, `forget` without `--prune`. No `check`, no `prune` (unbounded on a
  pre-existing repo). On failure persist everything **including the
  password** (the probe may have `init`ed a repo with it), withhold only
  `Enabled`, report the restic error verbatim.
- Failure semantics: flock busy = skip, not a failure. Free-disk, pause/
  stage, local `cat config`/`init`/`backup` = **fail the run**. Unpause
  failure = fail, keep the Staging File, app left paused (an emergency).
  Offsite leg = log, run **succeeds** (the common case with a flaky uplink;
  the staleness signal is the surface). `forget --prune` = log, run
  succeeds. `check` = log + record, run succeeds, **alarms separately**.
  Staging delete = log (fixed path, next run overwrites).
- **Nothing resumes** (no checkpoint under ~10 min / 50k blobs): retry is a
  full redo; never promise resumability.
- Stale locks: recovery is **evidence-triggered only** - on a lock-error
  failure, `unlock --remove-all` against that repo, warn-log, retry once.
  Never scheduled (a shared offsite repo may hold another host's live lock).
  Offsite `-tmp-` leak accepted as a named ceiling.
- Concurrency: a host flock at `/var/lib/once/backup.lock`
  (`LOCK_EX|LOCK_NB`), taken by the daemon run and manual `once backup`
  alike - Once's first lock primitive. Daemon busy = log + skip tick; CLI
  busy = "a backup is in progress", exit non-zero.

### Restore (issues/08)

- One verb. `once restore <arg>`: existing file = today's flow; otherwise an
  app name, restored from `--repo <url>` or the configured repo (default
  **local**, never auto-falling-back to offsite; `--repo` overrides).
- Discovery before trust: `once restore --repo <url>` with no app lists the
  repo (app, snapshot count, newest date, image). `--list` gives per-app
  history (short id, date, image); `--snapshot <short-id>` picks older than
  latest.
- Credentials: password by no-echo prompt or `RESTIC_PASSWORD`; backend env
  values from Once's environment; never argv.
- Mechanics: `restic restore <snap>:/staging --target <staging-dir>` (not
  `dump` - no container stdout plumbing), then `os.Open` and the existing
  `ns.Restore`. Reuses the staging dir, free-disk check, flock, `runRestic`.
- Prints its pick (app, image, snapshot time, original host) and proceeds
  with **no y/n** - restore is additive. Completion line names app, snapshot
  id, date, image.
- `ErrHostnameInUse` refusal kept; message names the colliding app and
  `once app destroy <name>`. No `--host` override (rollback fog).
- The App Profile travels in `once.application.json` for free; naming falls
  back `BaseName` then `NameFromImageRef`; restored Vaultwarden mounts
  `/data` and probes `/alive`.
- After a successful remote restore, one prompt: "Resume backups to this
  repo? [Y/n]" - yes runs the `backup enable` path wholesale (local `init`
  with `--copy-chunker-params` from the remote, probe, custody as needed);
  declining prints the one-liner.
- Verification = the deploy gates (readiness on the Profile's health path,
  `verifyHTTP` 2xx) plus the printed snapshot date. CLI-only; no TUI restore.

### Observability

Four facts surface in `once backup status` and the dashboard: offsite
staleness (the snapshots query), last `check` result per repo, the
same-device warning, and configured-but-not-enabled with its last error.
Wording and layout are decided at implementation (issue 17), as is offsite
upload progress through the ADR-0012 adapter (the only step a user waits on).
`State` gains `LastCheck map[string]OperationResult` keyed `local`/`offsite`;
`AppState.LastBackup` stays a single result meaning **the local leg**.

## Testing Decisions

Good tests assert behaviour at the seams - the Docker client interfaces
(ADR-0009) and the filesystem - never restic's internals. The silent failures
get explicit tests (issues/07):

1. Retention grouping: two runs of one app land in **one**
   `--group-by host,paths` group.
2. `--host` and the staging path are constant across time/container/run
   (unit test on the argument builder).
3. Free-disk check fires below 2x baseline and is skipped at zero baseline.
4. The enable probe leaves no snapshot behind.
5. A failed run does not retry inside an hour; a successful one not inside
   24h.
6. The flock is honoured: a manual export during a run exits non-zero.

Plus: restore of one gzipped and one plain archive; App Profile label
round-trip and zero-value compatibility; TUI forms driven by named targets
(existing driver doctrine); custody flow unit-tested per frontend with the
no-TTY fallback; the phone-page handler covered by httptest (token burn, 410,
constant-time compare).

**Acceptance test** (integration suite, no cloud credentials): deploy the
real `vaultwarden/server` image, write vault data, back up to a local repo
and to a second local directory standing in for offsite (populated by the
second `backup` leg, not `restic copy`), restore from that second repo into a
clean namespace, assert the vault is intact (deploy gates pass on `/alive`,
restored data matches).

## Out of Scope

- Whole-install recovery in one command (per-app restore only; today's
  archives capture no proxy or namespace settings).
- Backend-specific configuration UI for S3/B2/etc - any restic URL + env.
- Configurable retention.
- Migrating existing tar.gz directories into a repo.
- Vaultwarden on MySQL/Postgres, or `DATABASE_URL`/`ATTACHMENTS_FOLDER`
  pointed outside `DATA_FOLDER` (volume-only backup insufficient).
- Tailscale Funnel's dual origin vs one `DOMAIN` (WebAuthn is origin-bound).
- Restore-in-place / rollback, TUI restore, streaming rework (`parseBackup`
  buffers the payload; staging adds a full-size disk copy - a known,
  pre-existing ceiling).

## Release Notes

- Manual export files get bigger: `once backup <host> <file>` now writes
  plain tar. Existing `.tar.gz` archives stay restorable forever.
- A failed backup or update now retries hourly instead of every 5 minutes.
- New catalog deploys of Campfire are named `campfire` (was `once-campfire`);
  existing apps keep their name.
- `TrimBackups` and scheduled tarball dumps retire; the backup directory
  setting is replaced by `once backup enable`.

## Further Notes

- First real S3/B2 round-trip is an implementation-time check (credentials
  were unavailable during research).
- ADR-0013 gets one amendment line when the custody flow ships: reading a
  secret in stays TUI-only; handing one out reuses the shared custody
  component under the CLI's existing sequential-program pattern.
- Two Once installs may share one offsite repo: `--host {namespace}` keeps
  their retention groups independent.
- Implementation issues: 12 (tracer: Vaultwarden deploys + local backup),
  13 (enable flow: probe, custody screen, TUI), 14 (QR delivery), 15
  (offsite leg), 16 (restore from repo), 17 (status + dashboard), 18
  (acceptance test).
