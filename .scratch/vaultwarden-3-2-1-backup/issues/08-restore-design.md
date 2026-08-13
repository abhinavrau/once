# Design: restoring from a repo, including onto a brand new install

Status: open
Labels: wayfinder:grilling
Blocked by: 02 (resolved), 04 (resolved), 06

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

(pending)
