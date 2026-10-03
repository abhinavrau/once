# 01 - A killed Deploy leaves its new App Container running

**Status:** done

**What to build:** When a Deploy or auto-update is interrupted before it either
promotes or cleans up its new App Container (daemon killed, host reboot), the
next daemon start finds that orphan and removes it. Nothing keeps an App
Container that never became the active one.

## Evidence

Observed live on 2026-10-02:

- The background daemon started a vaultwarden auto-update at 13:09:26.
- The host rebooted at 13:09:46. systemd SIGKILLed the daemon after the stop
  timeout, mid-deploy.
- The new container `once-app-vaultwarden-bd38ae-c83da2` survived with
  `restart=always`. It was never healthy, because its mounts were wrong, so
  it restart-looped about 190 times over the next day. The old container kept
  serving.
- `once list` reported vaultwarden as `stopped`, because it was reading the
  orphan instead of the healthy active container.

On a normal failure the deploy path removes the new container. Only an
interrupted deploy leaks it.

## Acceptance criteria

- [x] On daemon start, an App Container that is not the Application's active
      container and is not part of an in-flight deploy is removed.
- [x] `once list` / `once status` report the active container's state, never a
      stray sibling's.
- [x] Covered by a test that leaves a second, unpromoted container behind and
      asserts the sweep removes it while the active one keeps running.

## Comments

Done.

- **Active App Container** (now in CONTEXT.md): the container the app's Proxy
  route targets (`kamal-proxy list`), else - no route (services topology,
  Tailscale-disabled tailnet) or an unreadable Proxy - the oldest. Not
  "newest wins", and not "oldest running": a Deploy switches the Proxy only
  once its new container is healthy, so the route is the promotion record.
  That reads the incident right (route still on the old container), finishes
  a Deploy killed after the switch instead of rolling it back, and keeps a
  user-stopped app reading as stopped beside a restart=always stray. The
  Proxy is read only when an app has more than one container, so a normal
  `once list` costs nothing extra.
- `restoreState` (list/status/TUI) and `ContainerName` (Stop/Start/backup/
  update's image compare) both pick through `activeContainers`.
- `Namespace.RemoveStrayContainers(ctx, now)` runs at daemon start and at the
  top of every check. Race handling: an app with any container younger than
  15 min (`strayContainerGrace`, well past the longest a Deploy waits on its
  new container) is skipped, so a deploy in flight in another process is
  never touched; the per-check sweep removes it once settled. The sweep never
  re-registers routes, so it cannot fight a concurrent Deploy's Proxy switch,
  and containers created after its listing are never candidates. In the
  incident (reboot 20s after create) the start sweep skips and the first
  check past the grace removes the stray.
- Tests: unit `TestActiveContainer`, `TestStrayContainers`,
  `TestParseRouteTargets`, `TestRestoreStateKeepsActiveContainerPerApp`;
  integration `TestRemoveStrayContainersAfterKilledDeploys` covers a
  restart-looping unpromoted sibling and a sibling the Proxy was already
  switched to.
- Open: a stray under the services topology (no Proxy route) relies on the
  oldest fallback, untested against Docktail. A daemon killed mid-backup
  leaves the app container paused - separate issue.
