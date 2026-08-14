# 16 - Restore from a repo, bare-metal first

Status: ready-for-agent

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

- [ ] On a clean namespace with only a repo URL and password: discovery
      listing, then restore of the newest Vaultwarden snapshot deploys it
      with `/data` mounts and `/alive` probe (Profile round-trips via
      `once.application.json`)
- [ ] `--snapshot` restores an older snapshot; the pick and completion
      lines print as specced
- [ ] Configured install defaults to the local repo; no silent offsite
      fallback
- [ ] Host collision refused with the specced message; restore is additive
- [ ] Resume-backups prompt enables backups end to end after a remote
      restore
- [ ] Lint, unit, and integration tests pass
