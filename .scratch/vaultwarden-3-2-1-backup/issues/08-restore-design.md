# Design: restoring from a repo, including onto a brand new install

Status: closed
Assignee: arau
Labels: wayfinder:grilling
Blocked by: 02 (resolved), 04 (resolved), 06 (resolved)

## Question

Specify restore from a restic repo, with bare-metal recovery as the primary case:
the original host is gone, the user has a fresh machine, a fresh Once, the repo
URL, and the password. Nothing else.

The good news is that `once restore <filename>` already does most of it - it
calls `ns.Setup` (booting network and proxy itself) then `ns.Restore`, which
creates a brand new app from the archive's `once.application.json`, needing only
a Docker daemon (restore.go; namespace.go:446-478). So restore-from-repo is:
pull the archive out of restic into staging, hand it to the flow that exists.
Specify:

- **CLI surface.** How the user names the repo, supplies the password, and picks
  a snapshot. Locked: per-app restore, not whole-install.
- **Discovery before trust.** On a fresh host there is no config, so the user
  must be able to list what is in the repo before restoring anything -
  `restic snapshots --json` filtered by tag, rendered as apps and dates. Specify
  that listing and its output.
- **Snapshot selection.** Latest by default; how an older one is chosen. What the
  user sees to confirm they picked the right one.
- **Credential entry.** Password by prompt or env (never argv, per ADR-0013), and
  backend credentials for the remote repo - which the user also has to have kept.
  Flag this: the offsite copy is only recoverable if the user kept both the
  password *and* the backend credentials. Does ticket 06's display-once flow
  need to cover the credentials too?
- **Which repo.** Bare-metal recovery reads the *remote* repo, since the local
  one died with the host. Confirm nothing in the flow assumes a local repo
  exists.
- **Bootstrapping the repo settings.** After a successful bare-metal restore, is
  the install now configured for ongoing backups to that repo, or does the user
  configure it again? Decide, since silently not backing up after a recovery is a
  trap.
- **App Profile round-trip.** The restored app must come back with its data path,
  health path, and URL env var (ticket 04) - confirm they travel in the archive
  and that the restored Vaultwarden mounts `/data` and probes `/alive`.
- **Host collisions.** `ns.Restore` refuses if the host is taken
  (`ErrHostnameInUse`, namespace.go:453) and always creates a new app via
  `UniqueName`. Specify what the user sees, and what happens when restoring an
  app that already exists on this install.
- **Post-restore verification.** How the user knows the vault is actually intact
  rather than merely deployed.
- **Backward compatibility with gzipped archives.** Ticket 05 dropped gzip, so
  `parseBackup` (namespace.go:620) must sniff the gzip magic bytes (`0x1f 0x8b`)
  instead of gunzipping unconditionally. Archives written by today's Once are
  gzipped and are sitting on users' disks, so restore must read both. Cover it
  with a test that restores a gzipped archive and a plain one.

## Resolution

All recommendations accepted as proposed. Grilled 2026-08-14.

### CLI surface

One verb: `once restore` grows repo awareness, no new subcommand.

- `once restore <arg>`: if `<arg>` is an existing file (`os.Stat`), today's
  file-path flow, unchanged. Otherwise `<arg>` is an app name and `--repo` (or a
  configured repo) is required.
- `once restore --repo <url>` with **no app arg** is the discovery listing: one
  line per app in the repo - name, snapshot count, newest snapshot date, image
  ref from the newest snapshot. This is the discovery-before-trust step on a
  fresh host, and it makes the bare invocation helpful instead of an error.
- `once restore <app> --repo <url> --list`: per-app snapshot history - short id,
  date, image.
- `--snapshot <short-id>` picks an older snapshot; latest is the default.
- `--repo` accepts either a local path or a remote restic URL; the flow is
  repo-agnostic and nothing assumes a local repo exists (bare-metal reads the
  remote directly).
- On a configured install, bare `once restore <app>` defaults to the configured
  **local** repo. Never auto-falls-back to offsite - silently switching hides
  which copy was trusted. `--repo` overrides.

### Credentials

- Password: interactive no-echo prompt, or `RESTIC_PASSWORD` when set. Never
  argv (ADR-0013).
- Backend credentials: values read from Once's environment, same as
  `backup enable`.
- The ticket-11 custody screen must carry the full recovery kit: password,
  offsite URL, and the backend env var **names** (never values - the user
  already holds those at their provider). Without the URL and names, a saved
  password still cannot mount the repo. Noted on ticket 11.

### Selection and confirmation

Latest by default. Before restoring, print the pick - app name, image ref,
snapshot time, original host - then proceed with **no y/n prompt**: restore is
additive (new app, refuses host collisions), so there is nothing to destroy.
Completion line: `Restored vaultwarden from snapshot a1b2c3d4 (2026-08-10,
vaultwarden/server:1.32)`.

### Mechanics

`restic restore <snap>:/staging --target <staging-dir>` (subfolder form,
verified byte-exact in ticket 02), then `os.Open` the staged file and hand it
to the existing `ns.Restore`. Not `restic dump` - that would stream a multi-GB
archive through container stdout plumbing Once does not have. Reuses ticket
07's staging dir, free-disk check, and host flock. Repo snapshots are always
plain tar; the gzip magic-byte sniff in `parseBackup` (ticket 05) covers old
file archives, tested with one gzipped and one plain archive.

### App Profile round-trip

The Profile is a field on `ApplicationSettings`, which is
`once.application.json` in every archive - it travels for free. The restored
app deploys with `DataPath` mounts and `HealthPath` probe via the ticket-04
deploy path; naming uses `BaseName` (`UniqueName(profile.BaseName)`, falling
back to `NameFromImageRef`). Acceptance test asserts the restored Vaultwarden
mounts `/data` and probes `/alive`.

### Host collisions

Keep the `ErrHostnameInUse` refusal. Message names the colliding app and the
`once app destroy <name>` command. No `--host` override - changing identity
mid-restore touches `DOMAIN`/WebAuthn semantics, which belongs to the
restore-in-place (rollback) fog, not this ticket.

### Bootstrapping ongoing backups

After a successful restore from a remote repo, one prompt: "Resume backups to
this repo? [Y/n]". Yes runs the `backup enable` path wholesale - fresh local
repo `init` with `--copy-chunker-params` from the remote (so `copy` backfill
works), ticket-07 validation probe, settings persisted. Declining prints the
`backup enable` one-liner. Silently not backing up after a recovery is the trap
this closes.

### Post-restore verification

The existing deploy gates are the definition of "vault intact": kamal-proxy
readiness on the profile's `/alive` (which opens the DB) and `verifyHTTP`'s
2xx, plus the printed snapshot date. No extra verification machinery; the
acceptance test provides the deeper proof.

### TUI

CLI-only for this MVP. Bare-metal recovery is inherently a terminal moment; a
dashboard restore flow waits for the rollback fog to graduate.
