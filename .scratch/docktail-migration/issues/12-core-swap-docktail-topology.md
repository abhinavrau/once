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

**Status:** done

- [x] Enable with no host tailscaled boots sidecar + Docktail; with a host tailscaled boots only Docktail and uses the host socket
- [x] Sidecar runs unprivileged userspace networking (no TUN device, no NET_ADMIN, no host networking) per the trimmed configuration research validated in issue 02
- [x] Apps, and the Once Admin Web App, carry `docktail.service.*` labels; per-app opt-out produces no labels for that app
- [x] TailscaleSettings round-trips with the new mode field and without APIKey; no loopback port is published
- [x] DomainSuffix is read from the local daemon and persisted; URLs display without any live lookup
- [x] Disable removes Docktail and the Once-managed sidecar (state volume retained) and strips labels via Roll
- [x] Integration suite covers the enable/disable lifecycle (containers, volumes, labels, opt-out) with no real tailnet; TUI enable-form driver updated in the same change
- [x] ADR-0002 written; ADR-0001 marked superseded
- [x] Lint, unit, and integration tests pass

## Comments

2026-07-13: Implemented. `Tailscale` (internal/docker/tailscale.go) now manages the
Docktail topology: `once-docktail` (ghcr.io/marvinvr/docktail:1.5.0, Docker socket
read-only) plus, when no host tailscaled socket is detected (dir overridable via
ONCE_TAILSCALED_DIR for tests), the unprivileged `once-tailscale` sidecar
(tailscale/tailscale:v1.88.4, TS_USERSPACE=true, state + socket volumes, auth key
minted with the exact probe shape via the shared createTailscaleAuthKey). The Magic
DNS suffix is read from the ticket-10 daemon seam at enable with a bounded 20s wait
and self-heals lazily via DomainSuffix() if the join was slow. WaitForFunnelActive
was repointed at the daemon's serve status (any-funnel-active; per-app attribution
is ticket 15). `once tailscale status` interim: prints suffix-computed service URLs
(states are ticket 14). Deviation from "module goes dead but is not deleted":
dropping APIKey forced the tsdproxy lookup-API/config/boot chain out (it no longer
compiled); the pure remnants (tsdproxyLabels, scanRegistrationError) stay for
ticket 20, and Destroy sweeps legacy once-tsdproxy containers/volumes. Headscale
registration/data-path/lookup tests were deleted; the suite now covers the
enable/disable lifecycle, host mode, opt-out, and teardown against headscale via
the control seam (sidecar-join coverage effectively included ahead of ticket 18).
Integration suite could not be executed on this machine (no Docker daemon
installed); it compiles and vets clean - run `make integration` on a Docker host
before closing.

2026-07-13 (later): Integration suite executed with Docker installed. First run
surfaced one real gap: the three sidecar-mode tests picked up the dev machine's
own tailscaled via the default socket path and ran enable in host mode (no
sidecar booted). Fixed with a forceSidecarMode test helper that points
ONCE_TAILSCALED_DIR at an empty temp dir. Full suite now green (31 tests), lint
and unit tests green. Closing.
