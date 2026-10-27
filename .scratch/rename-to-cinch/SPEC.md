# Spec: Rename Once to Cinch

Status: ready-for-agent

The build-ready output of the rename-to-cinch wayfinder map (MAP.md). It pulls
together tickets 01, 04 and 05; their resolution comments hold the reasoning.

## Problem

This repo is a hard fork of basecamp/once that is becoming its own product,
Cinch. The binary, module, stored identity, docs and distribution all still
say "once", and some still point at Basecamp's infrastructure: self-update
pulls basecamp/once releases, and the installer is Basecamp's `get.once.com`.

## Solution

Rename everything current to Cinch in one branch. Leave historical records as
they are. Point distribution at `abhinavrau/cinch` and `get.cinch.host`. Ship
it as `v0.2.0`.

## Ground rules

- **No migration.** Cinch never reads, adopts or cleans up anything `once`
  left behind: containers, volumes, labels, units, state dirs, tailnet
  devices, backups. No compatibility code, no legacy fallbacks. A `once`
  backup archive does not restore into Cinch.
- **No blind find-and-replace.** "once" is also an English word ("handed over
  exactly once", "retrying once", `replyOnce`). Rename by the patterns in the
  tables below, then review every remaining lowercase "once" by hand.
- **Capitalization:** "Cinch" in prose, `cinch` for the command and
  identifiers, "CINCH" only where the wordmark is uppercase today.
- **Historical records stay as written:** ADRs 0002-0018, closed `.scratch`
  tickets and PRDs, `docs/research/`, `.lavish/`, and commit history.
- **Basecamp's names stay:** catalog images (`ghcr.io/basecamp/once-campfire`
  etc. in internal/catalog), and "ONCE" when it means Basecamp's product line.

## Old -> new

### Code and build

| Old | New | Where |
|---|---|---|
| module `github.com/basecamp/once` | `github.com/abhinavrau/cinch` | go.mod, every import, Makefile:6 LDFLAGS |
| binary / cobra root `once` | `cinch` | internal/command/root.go:27, cmd/once -> cmd/cinch |
| release assets `once-<os>-<arch>` | `cinch-<os>-<arch>` | Makefile:45,48; internal/version/updater.go (`once-%s-%s`) |
| `api.github.com/repos/basecamp/once/releases/latest` | `.../repos/abhinavrau/cinch/releases/latest` | internal/version/updater.go:17 |
| `.once-update-tmp` | `.cinch-update-tmp` | internal/version/updater.go |
| `once version` prints the bare version | `cinch version` prints `cinch <version> (cinch.host)` | internal/command/version.go; the installer keys on `(cinch.host)` |
| secretspec project `once` | `cinch` | secretspec.toml |

### Stored identity (Docker, Tailscale, host)

| Old | New | Where |
|---|---|---|
| label key `once` | `cinch` | internal/docker/label.go:9 |
| default namespace `once` | `cinch` | internal/docker/namespace.go:26. Everything derived from the namespace follows: `cinch-proxy`, `cinch-app-<name>-<id>`, `cinch-docktail`, `cinch-tsdproxy`, `cinch-background` service, data volumes |
| `once-admin` container, `/run/once-admin`, `once-admin.sock`, `once-admin-nginx.conf`, response `once-admin ok` | `cinch-admin`, `/run/cinch-admin`, `cinch-admin.sock`, `cinch-admin-nginx.conf`, `cinch-admin ok` | internal/docker/admin.go |
| sidecar `once-tailscale` | `cinch-tailscale` | internal/docker (tailscale sidecar) |
| default Tailscale tag `tag:once` (and bare `once` in the tag field) | `tag:cinch` / `cinch` | internal/docker/tailscale*.go, internal/ui/tailscale_policy_screens.go:145, policy snippets |
| probe Service `svc:once-probe`, `once-serve-probe://invalid`, "Once credential probe - safe to delete" | `svc:cinch-probe`, `cinch-serve-probe://invalid`, "Cinch credential probe - safe to delete" | internal/docker/tailscale_services.go:99 and the probe code |
| `/var/lib/once/backups`, `/var/lib/once/backup.lock` | `/var/lib/cinch/backups`, `/var/lib/cinch/backup.lock` | internal/docker backup code |
| `.once-probe`, "once backup validation probe" | `.cinch-probe`, "cinch backup validation probe" | internal/docker/backup_probe.go:17 |
| backup archive entries `once.application.json`, `once.volume.json` | `cinch.application.json`, `cinch.volume.json` | internal/docker/application_backup.go:26-27 |
| `once-state.json` | `cinch-state.json` | internal/docker/proxy.go:25 |
| `once-recovery`, `once-recovery-handoff.json`, `/once-recovery-cert.pem`, `/once-recovery-key.pem` | `cinch-recovery...` | internal/handoff, internal/docker |
| `once-userstats.bin`, `once-userstats.json` | `cinch-userstats.bin`, `cinch-userstats.json` | internal/userstats/store.go:22,24 |
| state dir `~/.local/state/once` (and `$XDG_STATE_HOME/once`), `once.log` | `~/.local/state/cinch`, `cinch.log` | internal/logging/log.go:69,77 |
| systemd `Description=Once background tasks (%s)` | `Cinch background tasks (%s)` | internal/service/systemd.go:16 |
| launchd label prefix `com.basecamp.` | `host.cinch.` | internal/service/launchd.go:15 |
| proxy image `abhinavrau/kamal-proxy:once-02` | `abhinavrau/kamal-proxy:cinch-02` (same digest, retagged) | internal/docker/proxy.go:21 |
| restic offsite example `s3:s3.amazonaws.com/bucket/once` | `.../bucket/cinch` | help text and errors |

### Environment variables

Every `ONCE_*` becomes `CINCH_*`, with no fallback to the old name:

`ONCE_ADMIN_RUNTIME_DIR`, `ONCE_BIN`, `ONCE_INTERACTIVE`, `ONCE_NO_SELF_UPDATE`,
`ONCE_REDUCED_MOTION`, `ONCE_TAILSCALE_AUTH_KEY`, `ONCE_TAILSCALE_CLIENT_SECRET`,
`ONCE_TAILSCALE_CONTROL_URL`, `ONCE_TAILSCALED_DIR`, and the test-only
`ONCE_TEST_KEY_ID`, `ONCE_TEST_SECRET`, `ONCE_TEST_TS_BACKEND`,
`ONCE_TEST_UNSET_VARIABLE`. Also the `ONCE_TAILSCALE_` prefix wherever it is
matched, secretspec.toml, the systemd unit and launchd plist env injection, and
CI.

### Go identifiers

Rename identifiers where "Once" is the product name: `ErrOlderOnceSetup`,
`assertOnceAdminServesTheDaemon`, `onceNodeAttr`, `onceGrant`, `onceApprovers`,
`sweepOnceSettled`, `TestOnceAdmin*`, `TestNodesBuildConfigTargetsOnceProxy`,
`TestDeleteTaggedServicesDeletesOnlyOncesServices`, and any others of that
kind.

Keep identifiers where "once" means "one time": `replyOnce`,
`TestWaitFor*AnnouncesPhaseOnce`, `TestApp_QuitExitsOnceTheBusyScreenGoesIdle`,
`TestNodeRouteHostWithoutAWaitReadsOnce`,
`TestUIInstallBackupCheckboxOffersOverlayOnce`.

### User-facing text

| Old | New | Where |
|---|---|---|
| `` `once <subcommand>` `` in help, errors, hints | `` `cinch <subcommand>` `` | internal/command, internal/docker, internal/ui |
| "Once" as the product name in copy | "Cinch" | same |
| wordmark "ONCE" | "CINCH" | internal/ui/styles.go:76 breadcrumb; internal/ui/custody.go:49,478 "ONCE RECOVERY KIT"; internal/handoff/page.go:91 (and its `.once` CSS class) |
| password-manager entry `once backups - <host>` | `cinch backups - <host>` | internal/handoff/page.go |

## Distribution

- **Installer:** replace installer/ with one static `install.sh` served at
  `https://get.cinch.host`. Delete installer/main.go, main_test.go, go.mod,
  go.sum, Dockerfile and config/deploy.yml. The script:
  - downloads `cinch-<os>-<arch>` from the latest `abhinavrau/cinch` release
    and installs it to `/usr/local/bin/cinch`;
  - takes an optional image to install as an argument, replacing the
    templated `get.once.com/<image>` paths:
    `curl https://get.cinch.host | sh -s -- <image>`;
  - **foreign-binary guard:** if `cinch` is already on PATH, reuse it only if
    `cinch version` output contains `(cinch.host)`. Otherwise refuse with a
    message naming that path, and never overwrite it. (cinch.sh and
    cinchcli.com ship their own `cinch` binaries; cinchcli.com uses
    `/usr/local/bin/cinch`.)
- **Hosting:** a static file (GitHub Pages or Cloudflare) on `get.cinch.host`.
  Which one is an execution detail.
- **Self-update** only replaces `os.Executable()`, so it needs no guard. It
  only needs the new URL and asset name from the table.
- **Versioning:** the first Cinch release is `v0.2.0`, above every upstream
  `v0.1.x` tag. The upstream tags stay.

## Docs

- **README:** Cinch name, `curl https://get.cinch.host | sh`, and a line
  saying it is forked from basecamp/once. Keep MIT-LICENSE and its
  copyright notice unchanged.
- **AGENTS.md, CONTEXT.md** (the glossary, including the `once` label entry,
  Namespace default, and container names): rename to match the code.
- **New ADR-0019 "Once is renamed Cinch"** in docs/adr/: the decision (hard
  fork, no migration, history left as written), and a condensed old -> new
  table so readers of ADRs 0002-0018 can translate.
- **Open `.scratch` tickets:** rename. Closed ones: leave as written.

## Order of operations

The agent does steps 1-9 on a branch `rename-to-cinch`. Steps 10-13 need the
maintainer.

1. Module path: `go mod edit -module github.com/abhinavrau/cinch`, rewrite the
   exact import prefix `github.com/basecamp/once/` in every .go file, and
   update Makefile LDFLAGS. `make build` must pass.
2. Move cmd/once -> cmd/cinch; rename the cobra root, release asset names, the
   updater URL and temp file, and the `cinch version` output.
3. Stored identity: the tables above, one constant at a time.
4. Environment variables.
5. Go identifiers (product-name ones only).
6. User-facing text and the wordmark.
7. Installer rewrite and deletion of the Go server, with a shell test for the
   foreign-binary guard (a fake `cinch` on PATH without the marker makes the
   script refuse).
8. Docs: README, AGENTS.md, CONTEXT.md, ADR-0019, open tickets.
9. Verify: `make build`, `make test`, `make lint`, and `make integration`
   (under `flock /tmp/once-integration.lock`, then rename that lock path to
   `/tmp/cinch-integration.lock` in AGENTS.md). Run the leftover check below.
   Then `graphify update .`.
10. Maintainer: retag the proxy image with
    `docker buildx imagetools create -t abhinavrau/kamal-proxy:cinch-02 abhinavrau/kamal-proxy:once-02`.
11. Maintainer: rename the GitHub repo `abhinavrau/once` -> `abhinavrau/cinch`,
    update `origin`, and ask GitHub support to detach it from basecamp/once's
    fork network.
12. Maintainer: point `get.cinch.host` at the static host serving
    `install.sh`.
13. Merge, tag `v0.2.0`; the release workflow publishes the
    `cinch-<os>-<arch>` assets. Check with
    `curl https://get.cinch.host | sh` on a clean machine.
14. Local dev: rename the workspace dir `~/code/once` -> `~/code/cinch`,
    rebuild graphify-out, update treehouse.toml if it names the path, and let
    deja re-index the new path.

## Leftover check

The rename is done when this prints only lines a human has judged to be
English "once", Basecamp references, or historical records:

```
git grep -nIi -w 'once' -- . \
  ':!docs/adr/000[2-9]*' ':!docs/adr/001[0-8]*' ':!docs/research' ':!.lavish' \
  ':!.scratch' ':!internal/catalog' ':!MIT-LICENSE'
```

plus `git grep -n 'ONCE_\|basecamp/once\|once-'` returning nothing outside
historical records, the README credit line, and catalog images.
