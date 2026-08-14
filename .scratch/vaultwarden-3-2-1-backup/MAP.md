# Wayfinder map: Vaultwarden on Once, with 3-2-1 restic backups

Labels: wayfinder:map

## Destination

A build-ready PRD (PRD.md in this directory) for an MVP in which a self-hoster
can deploy Vaultwarden on Once, have it backed up automatically under the 3-2-1
rule via the restic CLI, and restore it onto a brand new install after the
original host is gone. Implementation is a separate effort; this map ends at
the spec.

## Notes

- Domain vocabulary: CONTEXT.md. New concepts this effort introduces (Backup
  Repo, Offsite Copy, App Profile) must land in the glossary as part of the PRD.
- Existing backup code: internal/docker/application_backup.go,
  internal/command/backup.go, internal/command/restore.go,
  BackupSettings in internal/docker/application_settings.go:34.
- Catalog: internal/catalog/catalog.go (3 Basecamp Rails apps today).
- Relevant ADRs: 0009 (consumer-side docker client interfaces - constrains how
  restic gets invoked), 0012 (progress rendering per frontend), 0013 (secrets
  reach Once through the environment, never argv - the restic password must
  follow this).
- Skills: /grilling and /domain-modeling for HITL tickets; /research for AFK
  research tickets.

### Established facts (verified during charting, do not re-derive)

- `once deploy vaultwarden/server:latest` today **hard-fails and destroys the
  volume**. Two gates: kamal-proxy's readiness probe, then `VerifyHTTPOrRemove`
  which demands 2xx from `/up` and calls `Remove(ctx, true)` otherwise
  (application.go:46, 254-263, 490-513; namespace.go:248-253).
- `AppVolumeMountTargets = ["/storage", "/rails/storage"]` is a package `var`
  with no settings field, flag, or override (application.go:50-52, 536-546).
  Vaultwarden writes `/data`, so its DB would live in the container's writable
  layer and backups would archive an empty directory.
- `NameFromImageRef("vaultwarden/server:latest")` yields `server`, so the host
  becomes `server.localhost` (helpers.go:18-26).
- Vaultwarden: port **80** (`ROCKET_PORT=80` in the official image), health path
  **`/alive`** (unauthenticated, and it checks the DB connection), data at
  `/data`. `DOMAIN` unset silently disables WebAuthn 2FA and makes attachment
  links get guessed from request headers. `config.json` and `rsa_key.pem` hold
  plaintext admin token and SMTP credentials.
- Vaultwarden's image has **no `sqlite3` binary**. Its built-in
  `/vaultwarden backup` (SIGUSR1, `VACUUM INTO`) can only write into the DB's own
  directory and never rotates, so it would litter the data volume.
- Once's existing pause-and-copy is **already safe** for SQLite: `ContainerPause`
  freezes the container before `CopyFromContainer` walks the whole directory, so
  `db.sqlite3` and `db.sqlite3-wal` come out as a coherent pair - the condition
  SQLite's corruption doc requires.
- Once has **zero** stdin plumbing: no `AttachStdin`, no `ContainerAttach`
  anywhere. `execInContainer` (container.go:33-64) takes the concrete client and
  buffers output. ADR-0009 names exec-over-hijacked-connection as unfakeable at
  the client seam.
- `once restore <filename>` already does bare-metal recovery: it calls
  `ns.Setup` (boots network + proxy itself) then `ns.Restore(io.Reader)`, which
  creates a brand new app from the archive's `once.application.json`. It needs
  only a Docker daemon (restore.go; namespace.go:446-478).

### Decisions locked during charting (not tickets)

- Destination is a PRD, not shipped code.
- Per-app non-defaults (data path, health path, URL env var) live as new columns
  on the existing `catalog.App` struct, copied into `ApplicationSettings` at
  deploy so they persist in the `once` label. Settable by flag for arbitrary
  images. No app-manifest subsystem.
- restic **wraps** the existing archive stream rather than snapshotting the file
  tree, so every snapshot carries `once.application.json` and
  `once.volume.json` and feeds the existing `ns.Restore` unchanged.
- Bytes reach restic via a **staging file**, not stdin: write the archive to a
  staging dir, run the restic container with bind mounts for staging + repo,
  delete the staging file after. Ceiling: needs free disk equal to one backup.
- restic runs in a **container** (`restic/restic` image), never as a host binary.
- **One repo per install**, snapshots tagged `app:<name>`. Local repo on host
  disk is copy 2; a remote repo is copy 3 (offsite).
- **The offsite leg backs up twice, once to each repo - not `restic copy`.**
  Originally locked as `copy`; changed on ticket 03's evidence that `copy` makes
  the offsite repo structurally downstream of the local one (shared locks, shared
  disk), while the cost that justified `copy` was illusory - with staging, the
  pause window is 1x either way and only off-critical-path CPU doubles. The two
  repos are fully independent. Still `init` the remote with
  `--copy-chunker-params` so `copy` remains available as a one-shot backfill when
  a remote is enrolled late.
- Offsite target is **any restic repo URL + env vars**. No backend picker UI.
- Repo password is **user-held**: generated when backups are **configured**,
  printed once with a hard "store this off this machine" prompt, cached in a
  label so unattended backups run. Lose it, lose the backups, by design.
  Bare-metal restore takes it by prompt or env. Originally locked as generated
  at *first backup*; changed on ticket 06's finding that the first backup is
  normally the daemon on its 5-minute tick, with no human present, so the one
  moment recovery depends on would be shown to nobody.
- Backup settings live on the **proxy container's label**, as a sibling struct
  through the existing label codec. Needs an ADR line.
- Retention: fixed `--keep-daily 7 --keep-weekly 4 --keep-monthly 6 --prune`.
  Not configurable in the MVP. `TrimBackups` and scheduled tarball dumps retire.
- `once backup <host> <file>` manual export survives unchanged.
- The offsite leg runs in the same run, immediately after the local one, reading
  the same staging file (originally `restic copy`; see the amended entry above).
  Its failure logs but does not fail the run - **but offsite staleness must be
  surfaced** ("offsite copy is 6 days old") in status and the dashboard.
- Quiesce stays pause-and-copy. No catalog-declared backup command.
- `restic check` (metadata only, no `--read-data`) after each backup; result
  surfaced alongside the staleness signal.
- `AutoUpdate` stays on by default for Vaultwarden.
- Acceptance test: deploy the real `vaultwarden/server` image, write data, back
  up to a local repo, `restic copy` to a second local directory standing in for
  offsite, restore from that second repo into a clean namespace, assert the vault
  is intact.

## Decisions so far

<!-- one line per closed ticket: gist + link -->

- [Research: configuring kamal-proxy's health check path](issues/01-research-kamal-proxy-health-check.md) - **Gate clears.** The pinned `once-01` image accepts `--health-check-path` (default `/up`), verified empirically against a real Vaultwarden container: default deploy fails on `/up` 404, `--health-check-path /alive` succeeds. Also has `--health-check-interval/-timeout/-port/-host`, and `--target host:port` (bare host means port 80). Three consequences beyond the question: kamal-proxy **stops health-checking a single target once healthy and never resumes**, so `kamal-proxy list` is not a liveness signal; redirects are followed and judged on the final status, so a 302 to a 200 login page passes; and `verifyHTTP` is a second, independent kill switch that deletes the app *and its volume*, so fixing the proxy flag alone still destroys Vaultwarden
- [Research: restic CLI operations and the official container image](issues/02-research-restic-cli-operations.md) - Verified against restic 0.19.1. Workable, with four traps that must be designed around: **`forget --group-by` defaults to `host,paths`**, so a random container hostname or a date-stamped staging filename puts every run in its own group and retention silently keeps everything forever (pin the staging path, always pass `--host`); **`init` is not idempotent** and has no distinct exit code, so probe with `cat config` (10 = init, 0 = skip, 12 = wrong password); **stale locks cannot self-heal under Docker** in either hostname configuration, so `unlock --remove-all` must be an explicit recovery step; **`check` takes an exclusive lock** and has no distinct exit code for corruption, so Once must read `num_errors` from `--json`. Also: `prune` ignores `--json`, `--json` emits ~10 status lines/sec so Once must take the *last* `summary` line, and bind-mount uid is a one-way door - pick it once
- [Research: `restic copy` and the two-repo offsite topology](issues/03-research-restic-copy-offsite.md) - **Recommends backing up twice instead of `restic copy`**, contradicting the charting decision (see the amended entry above). `copy` makes the offsite leg structurally downstream of the local repo, so a wedged lock or a full disk takes both copies down - the exact dependency 3-2-1 exists to remove - and the assumed cost of backing up twice was wrong: with staging, the pause window is 1x either way, only off-critical-path CPU doubles. Still `init` the remote with `--copy-chunker-params`, which keeps `copy` available as a one-shot backfill when a remote is enrolled late. Also measured the archive-format question outright (see ticket 05), found `RESTIC_FROM_PASSWORD` works but is undocumented in `restic help copy`, that neither `backup` nor `copy` resumes under ~10 min, and that SIGKILL leaves `-tmp-` files invisible to both `prune` and `check`
- [Design: the App Profile seam - catalog columns to app settings](issues/04-app-profile-seam.md) - **App Profile** is a four-field comparable struct (`BaseName`, `DataPath`, `HealthPath`, `URLEnvVar`) in `internal/docker`, nested on both `catalog.App` and `ApplicationSettings`. **The zero value means Once's built-in behaviour**, so `DefaultApplicationSettings` keeps its single-source-of-defaults role and **no backfill is needed** - an old label with no `profile` key behaves exactly as today. A custom `DataPath` **replaces** both Rails mount targets rather than adding a third. `Resolve` returns the `catalog.App`, since a string cannot carry a Profile. Three findings changed the ticket's own framing: the **URL env var is computed, never stored** (`BuildEnv` always has the Application in hand, and Host/Exposure changes already roll the container), using the tailnet URL under Tailnet exposure plus a Deploy warning when it resolves empty; **restore needs no change at all** because `populateVolume`'s `/data` is paired with `BackupDataDir`, not the app's data path; and **user stats filter the Proxy's source address**, not the health path, which needs no per-app lookup table. The App Name had to move into the Profile too - `ns.Deploy` and `ns.Restore` overwrite `settings.Name` from the image ref, so `vaultwarden/server` would deploy as `server`; setting `BaseName` from `Alias` on every catalog entry also fixes Campfire, which deploys as `once-campfire` today
- [Decision: does the restic-wrapped archive stay gzipped?](issues/05-archive-format-compression.md) - **No. Drop gzip; the archive becomes plain `.tar`.** gzip makes restic re-add the whole archive every run (19.085 MiB/day against 981 KiB for plain tar), which would grow the repo by a full vault per day. The manual export changes format too, so only one format and one code path exist. `parseBackup` must sniff the gzip magic bytes so archives written by current Once stay restorable - without that, every existing backup breaks on upgrade. Staging path fixed at `/staging/<app>.tar`; `RESTIC_COMPRESSION` left at `auto` so restic compresses at chunk granularity

- [Design: configuring backups, and custody of the repo password](issues/06-backup-configuration-ux.md) - Backup Repo settings (`Enabled`, `LocalPath`, `OffsiteURL`, `OffsiteEnv`, `Password`) sit as a **top-level key** on the proxy label, which is what makes an older binary's rewrite carry them through `labelExtra` instead of destroying the password. **No observed state on any label** - Docker labels are immutable, so a `lastRun` there would recreate the Proxy on every run; the check result joins the existing state file inside the Proxy container, while user-facing staleness reads `restic snapshots`, because that file is a cache a Proxy roll erases. CLI is `once backup enable|disable|status` beside the unchanged two-arg export, with backend credential **names** passed by flag and values read from Once's own environment (ADR-0013). TUI puts install-wide settings in a dashboard-global overlay like `TailscaleForm`, leaves a single "Include this app in backups" checkbox per app, and asks that checkbox during install - ticking it with no repo configured opens the overlay inline, once. Its split of the password moment into a gating TUI and a printing CLI was **replaced in review by one shared path** that asks the user how to take custody - on screen, or by QR to their phone - with the type-back gate after either and a no-TTY fallback to today's print-and-banner ([ticket 11](issues/11-prototype-password-moment.md)). Two findings changed the shape: the password must be generated at **configure** time, not first backup (the amended entry above), and the **health path must not hang off the backup checkbox** - `verifyHTTP` deletes the volume whether backups are on or not, so a custom image is asked for it unconditionally, while the checkbox forces only `DataPath`. Local repo defaults to `/var/lib/once/backups` with a persistent warning when it shares a device with the Docker root, since that configuration is 3-2-1 in name only

- [Research: can a terminal show a scannable QR code for the repo password?](issues/10-research-terminal-qr-secret-handoff.md) - **Feasible and cheap, but gated on a five-minute phone test, because what is in doubt is the payoff, not the engineering.** Half-block rendering decoded through a real decoder and a camera model at every terminal cell metric tested; naive one-cell-per-module failed universally; `tailscale up --qr` already ships this exact technique. Cost is one MIT dependency (`piglig/go-qr`), ~25 lines of renderer, and a dedicated full-screen view. **The finding that reaches back into an undecided detail: the password's alphabet sets the size floor, not the library.** 64 mixed-case characters render 49x25 and do not fit the enforced 80x24 terminal floor at all; the same 320 bits as uppercase base32 render 41x21, because QR switches to alphanumeric mode - so the alphabet must be chosen before the password format is fixed. And the catch: no password manager ingests a raw-text QR (all four checked are TOTP-or-file only), and neither Apple nor Android documents a copy affordance for plain text, so a QR of the secret itself rests on undocumented phone behaviour. **Its "the URL variant is dead" conclusion was overturned in review and does not stand:** it holds for `once-admin`, which publishes no host ports, but not for Once, because the Proxy publishes 80 and 443 and the pinned kamal-proxy image accepts `--tls-certificate-path`/`--tls-private-key-path`. So the QR encodes a one-time URL to a page that puts the password in a form field, which is the payload both phone platforms handle well and the only one a password manager will save - over the tailnet URL when Tailscale is on, over a self-signed cert on the LAN IP when it is not, with the browser warning accepted deliberately and explained on screen before the user scans ([ticket 11](issues/11-prototype-password-moment.md))

- [Design: what one backup run does](issues/07-backup-run-design.md) - A Backup Run is **install-wide, not per-Application**: `checkBackup` leaves the per-app loop and becomes a sibling of `checkSelfUpdate`, staging and backing up each due Application in turn, then running **one** `forget --prune` and **one** `check` per repo - correct because ticket 02's pinned per-app staging path already puts each Application in its own retention group. Backups now run **before** auto-update, so the newest snapshot is of the known-good version. Staging goes to `<LocalPath>.staging` (a suffix, never a child of the repo), bind-mounted read-only, with a free-disk check against **2x the last archive size** recorded per Application - short disk fails the run loudly rather than skipping. restic runs as **one container per command** behind `runRestic`, digest-pinned, root uid forever, named cache volume, `--host {namespace}` as an explicit flag; it is a sixth `ContainerCreate` site, deliberately not factored with the two temp-container helpers, whose lifecycle is opposite. Three findings reached past the question: `operationDue` **retries a failed run every 5 minutes forever** (state.go:81 gates only successes), a pre-existing defect hitting Update too, fixed here with a 1h floor; **enabling backups now runs a validation probe first** and sets `Enabled` only if it passes, and that probe must be **synthetic** because ticket 06's own inline-overlay decision means the Application does not exist yet - persisting everything but `Enabled` on failure, password included, since a probe that reached `init` has already committed it; and a **host flock** is now Once's first lock primitive, because a manual export racing the daemon resumes the App Container under the other's copy and silently produces the torn SQLite pair the pause exists to prevent. Offsite failure logs but the run succeeds; `check` failure alarms separately from backup failure; stale locks are unlocked only on **evidence** of a lock error, never on a schedule, and the offsite `-tmp-` leak is accepted as a named ceiling

- [Design: restoring from a repo, including onto a brand new install](issues/08-restore-design.md) - One verb: `once restore` grows repo awareness, no new subcommand - an existing-file arg is today's flow, an app name plus `--repo` (or the configured repo) restores from restic. Discovery-before-trust is the same command bare: `once restore --repo <url>` with no app lists the repo (app, snapshot count, newest date, image), `--list` gives per-app history, `--snapshot <short-id>` picks older than latest. On a configured install the default repo is **local, never auto-falling-back to offsite** - silent fallback hides which copy was trusted. Password by no-echo prompt or `RESTIC_PASSWORD`, backend creds from the environment; **ticket 11's custody screen becomes a recovery kit**, adding the offsite URL and backend env var names (never values). Mechanics: `restic restore <snap>:/staging --target` into ticket 07's staging dir (not `dump` - no container stdout plumbing), then the existing `ns.Restore`; the Profile travels free inside `once.application.json`, naming falls back `BaseName` then `NameFromImageRef`. No y/n before restoring (additive, prints its pick), `ErrHostnameInUse` refusal kept with a message naming the app and `once app destroy` (no `--host` override - that is rollback fog), and after a remote restore one prompt "Resume backups to this repo?" runs `backup enable` wholesale with `--copy-chunker-params` from the remote. Verification is the deploy gates plus the printed snapshot date; CLI-only, no TUI flow

- [Prototype: the one-time password moment](issues/11-prototype-password-moment.md) - **The phone test passed, so both custody paths ship**: on iOS Safari the password-manager save prompt fires even on an origin whose self-signed-cert warning was clicked through, with one wording consequence (Safari often shows a blank page around the interstitial and needs a manual refresh - the pre-scan text must set that expectation). The reveal screen is **variant D**: a numbered copy/store/prove checklist with the bordered RECOVERY KIT block (password, offsite repo, credential names, local repo, created) under step 1, needing ~3 rows trimmed for the 80x24 floor. The gate is an unmasked, normalizing type-back with a loud skip hatch (password persists, `Enabled` stays false), and the clipboard round-trip loophole is accepted deliberately. ctrl+p copies the bare password, ctrl+k the whole kit; **mouse tracking is off on any screen showing a secret** so native selection works. The phone page holds the password in a form field with PRG-back-to-itself on save, copy buttons, and an explicit Done that burns the single-use 10-minute token. LAN IP is the default route's source address. Password is 32-char base32. The ADR-0013 tension dissolved by demonstration - the CLI already runs sequential bubbletea programs (cli_progress.go:58), so the custody flow is just another one; one amendment line at ship time. Prototype preserved on branch `prototype/password-moment`

## Not yet specified

- Progress reporting for long backup runs through the ADR-0012 progress adapter.
  Ticket 07 narrowed this to exactly one step: the **offsite upload**. The pause
  is sub-second and proportional to Application Data size alone, and everything
  else in a run is bounded - so the only place a user waits is the network leg,
  whose `--json` shape ticket 02 already established (stream, take the last
  `summary` line).
- Streaming rework. `parseBackup` buffers the entire volume payload into a
  `[]byte` (namespace.go:623) and the staging-file design adds a full-size disk
  copy, so Once is bound by backup size in both memory and disk. Pre-existing,
  but the ceiling gets lower once repos hold more than one app.
- Restoring into an existing app in place (rollback) rather than always creating
  a new one.
- What `once backup status` prints and what the dashboard shows. Four facts are
  now decided as things to surface - the offsite staleness signal (a
  `restic snapshots` query, not state), the last `check` result per repo, the
  same-device warning, and configured-but-not-enabled with its last error - but
  their wording and layout are not.

## Out of scope

- Whole-install recovery in one command (every app plus proxy and namespace
  config). Today's backup captures no proxy or namespace settings, and a
  "restore everything" that silently drops them is worse than an honest per-app
  flow. Per-app restore only.
- Backend-specific configuration UI for S3, B2, and friends. restic already
  speaks them; a picker is UI maintained for nothing.
- Configurable retention policy.
- Migrating existing directories of tar.gz backups into a restic repo.
- Vaultwarden on MySQL or Postgres, and installs that point `DATABASE_URL` or
  `ATTACHMENTS_FOLDER` outside `DATA_FOLDER`. A volume-only backup is not
  sufficient for those.
- Tailscale Funnel's dual origin. A funnelled Application is reachable at both
  its tailnet URL and its per-node Funnel URL, and WebAuthn is origin-bound, so
  one `DOMAIN` value cannot serve both. No App Profile field shape fixes one
  variable against two origins ([ticket 04](issues/04-app-profile-seam.md)).
- Implementation of the feature itself - this map ends at the PRD.
