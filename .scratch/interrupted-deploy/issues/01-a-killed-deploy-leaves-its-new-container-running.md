# 01 - A killed Deploy leaves its new App Container running

**Status:** needs-triage

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

- [ ] On daemon start, an App Container that is not the Application's active
      container and is not part of an in-flight deploy is removed.
- [ ] `once list` / `once status` report the active container's state, never a
      stray sibling's.
- [ ] Covered by a test that leaves a second, unpromoted container behind and
      asserts the sweep removes it while the active one keeps running.

## Comments
