# 16 - Restore from a repo, bare-metal first

Status: done

**What to build:** `once restore` grows repo awareness (issues/08; PRD
"Restore"). No new subcommand, CLI-only.

- `once restore <arg>`: existing file = today's flow. Otherwise an app name;
  repo from `--repo <url|path>` or the configured **local** repo (never
  auto-falling-back to offsite; `--repo` overrides).
- `once restore --repo <url>` with no app: discovery listing - one line per
  app (name, snapshot count, newest date, image from newest snapshot).
  `--list`: per-app history (short id, date, image). `--snapshot <short-id>`
  picks older than latest.
- Password via no-echo prompt or `RESTIC_PASSWORD`; backend values from the
  environment; never argv (ADR-0013).
- Mechanics: `restic restore <snap>:/staging --target` into the staging dir
  (never `dump`), `os.Open`, existing `ns.Restore`. Reuses `runRestic`, the
  flock, and the free-disk check. Works on a fresh install (`ns.Setup` boots
  network + proxy; nothing assumes a local repo or any config).
- Prints the pick (app, image, snapshot time, original host), no y/n;
  completion line names app, snapshot id, date, image. `ErrHostnameInUse`
  refusal names the colliding app and `once app destroy <name>`; no
  `--host` override.
- After a successful remote restore: "Resume backups to this repo? [Y/n]".
  Yes runs the enable path wholesale (fresh local `init` with
  `--copy-chunker-params` from the remote, probe, custody); no prints the
  `once backup enable` one-liner.

**Blocked by:** 15 (resume-backups needs the offsite-aware enable path).

- [x] On a clean namespace with only a repo URL and password: discovery
      listing, then restore of the newest Vaultwarden snapshot deploys it
      with `/data` mounts and `/alive` probe (Profile round-trips via
      `once.application.json`)
- [x] `--snapshot` restores an older snapshot; the pick and completion
      lines print as specced
- [x] Configured install defaults to the local repo; no silent offsite
      fallback
- [x] Host collision refused with the specced message; restore is additive
- [x] Resume-backups prompt enables backups end to end after a remote
      restore
- [x] Lint, unit, and integration tests pass

## Comments

Implemented on branch issue-16-restore. All evidence below is from
`TestRestoreVaultwardenFromRepo` (integration/restore_repo_test.go) unless
noted: one test that deploys the real `vaultwarden/server`, backs it up twice
to a local and an offsite repo with different vault contents, **tears the
install down**, and then works only from the repo path and the password.

- **Bare-metal discovery and restore.** `once restore --repo <path>` lists
  `vaultwarden-xxxxxx`, 2 snapshots, newest date, image; `once restore <app>
  --repo <path>` restores the newest onto a namespace with no applications
  and no backup configuration (asserted before the restore). The restored app
  has a single `/data` mount, a Profile equal to the catalog entry's (data
  path, health path, `DOMAIN`), and its vault file reads `vault-data-two`.
  The `/alive` gate is what the restore's own deploy waits on, so a broken
  probe is a failed restore, not a failed assertion.
- **Older snapshot.** `--snapshot <id>` (read out of `--list`'s output by the
  test, so the ids are the ones a user would copy) brings back
  `vault-data-one`. The pick prints app, repo, snapshot id, time, **original
  host**, and image before proceeding with no y/n; the completion line is
  asserted by regex against the specced shape.
- **Configured install defaults to local.** On the still-configured source
  install, bare `once restore` prints `Applications in <localPath>` and the
  output never contains the offsite path. Pointing `LocalPath` at a directory
  that no longer exists gives `no backup repository at <localPath>` - it does
  not quietly read the offsite copy instead.
- **Collision.** A second restore of the same snapshot fails with `hostname
  already in use: <app> already serves vaultwarden.localhost; remove it first
  with `once remove vaultwarden.localhost``, and the occupant is still there
  with its data. The message names `once remove <host>`, not the ticket's
  `once app destroy <name>` - that command does not exist in Once.
- **Resume backups.** `Namespace.ResumeBackupsAfterRestore` inits a fresh
  local repo with `--copy-chunker-params` from the recovered repo (the test
  compares `chunker_polynomial` across the two), runs the ticket-13 probe, and
  the run that follows lands the restored app in **both** repos. The CLI's
  prompt is TTY-only by construction; with no terminal the restore prints
  ``Run `once backup enable --offsite-url <repo>` `` instead, which the test
  asserts. The custody moment is the same `takeCustody` both frontends use.

Decisions worth carrying forward:

- **Snapshots gained an `image:<ref>` tag.** restic keeps no other
  per-snapshot metadata, and the listing has to name an image before any
  archive is open. Tags play no part in `forget`'s host,paths grouping, so an
  upgraded app keeps its single retention group (`TestResticBackupArgsAreConstant`).
  Snapshots written before this change list with an empty image and restore
  normally.
- **Free disk on restore** reads `summary.total_bytes_processed` off the
  snapshot itself (restic >= 0.17) and reuses `backupDiskCheck`. A snapshot
  with no summary means no baseline, which skips the check rather than
  refusing.
- **The app name in the repo is the source install's unique name**
  (`vaultwarden-a1b2c3`), because that is what the `app:` tag carries. The
  discovery listing is therefore load-bearing: it tells the user exactly what
  to type. Not-found names an app and points at the listing.
- **A question about a repo mounts no Staging Directory** and a filesystem
  repo that does not exist is refused by name - both found by the first run
  of the integration test, where Docker's missing-bind error named
  `/var/lib/once/backups.staging`, a path the user never typed.
- **The file form still wins** when the argument is an existing path, unless
  `--repo`/`--list`/`--snapshot` is set. Pure `os.Stat` as specced would let a
  file that happens to share an app's name silently swallow a `--repo`.
- **ADR-0013** gained a second amendment line: `once restore` is the one CLI
  secret prompt, bounded by the case - no install holds the password, there is
  no TUI, and nothing is rendering progress. A typed password is normalized
  the way the custody gate normalizes it (dashes and case off a paper kit);
  `RESTIC_PASSWORD` is taken verbatim and is the escape hatch for a repo Once
  did not create.
- **`DefaultBackupLocalPath` is now a var**, like `BackupLockPath`, so the
  suite can stage a restore somewhere it can write.
