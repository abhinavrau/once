# 12 - Core swap: enable/disable runs Docktail with service labels

**What to build:** The tracer bullet. `once tailscale enable` (CLI and TUI) boots the Docktail topology instead of TSDProxy, and `once tailscale disable` tears it down cleanly:

- Enable detects a host tailscaled (socket directory present); otherwise it boots the unprivileged `once-tailscale` sidecar (userspace networking: no TUN, no NET_ADMIN, no host networking; pinned tailscale/tailscale image v1.86.0+; state volume; socket shared via volume). It then boots `once-docktail` (pinned image, Docker socket read-only, tailscaled socket mounted), following the existing supporting-container Boot doctrine: idempotent boot, existence of `once-docktail` is the source of truth for "Tailscale enabled".
- App Containers get `docktail.service.*` labels (enable, name = app name, port 80) instead of `tsdproxy.*`, injected/stripped by the existing Roll retrofit on enable/disable. The per-app tailnet opt-out is respected. The Once Admin Web App's nginx container gets the same service labels.
- TailscaleSettings keeps ClientID/ClientSecret/Tag, drops the APIKey field and the loopback-published API port, and gains a field recording host-vs-sidecar mode. Stored in the `once` label as before.
- The Magic DNS suffix is read from the local daemon (ticket 10's interface) at enable time and persisted in DomainSuffix; TailnetURL computation (`https://{app}.{suffix}`) is unchanged and app URLs in CLI/TUI render instantly from the stored suffix.
- Disable removes the Docktail container and any Once-managed sidecar and strips service labels from apps via a Roll.
- ADR-0002 is written, recording the Docktail / Tailscale Services architecture and marking ADR-0001 superseded.

The TSDProxy module goes dead but is not deleted (that is ticket 20). Integration and TUI form-driver tests are updated in this same change - the suite must be green when this lands; headscale-dependent registration tests that can no longer run are deleted here or skipped pending tickets 18/20.

**Blocked by:** 10 (local daemon interface), 11 (credential probe).

**Status:** ready-for-agent

- [ ] Enable with no host tailscaled boots sidecar + Docktail; with a host tailscaled boots only Docktail and uses the host socket
- [ ] Sidecar runs unprivileged userspace networking (no TUN device, no NET_ADMIN, no host networking) per the trimmed configuration research validated in issue 02
- [ ] Apps, and the Once Admin Web App, carry `docktail.service.*` labels; per-app opt-out produces no labels for that app
- [ ] TailscaleSettings round-trips with the new mode field and without APIKey; no loopback port is published
- [ ] DomainSuffix is read from the local daemon and persisted; URLs display without any live lookup
- [ ] Disable removes Docktail and the Once-managed sidecar (state volume retained) and strips labels via Roll
- [ ] Integration suite covers the enable/disable lifecycle (containers, volumes, labels, opt-out) with no real tailnet; TUI enable-form driver updated in the same change
- [ ] ADR-0002 written; ADR-0001 marked superseded
- [ ] Lint, unit, and integration tests pass
