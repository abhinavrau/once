Status: done

# App container `tsdproxy.*` label injection + retrofit roll on enable/disable

## Parent

- PRD: `.scratch/tailscale-integration/PRD.md`
- ADR: `docs/adr/0001-tailscale-integration.md`

## What to build

Make Once apps appear on the tailnet under their own Magic DNS names by attaching `tsdproxy.*` labels to app containers, and retrofit existing apps when Tailscale is toggled.

- When Tailscale is enabled, app container creation attaches:
  - `tsdproxy.enable=true`
  - `tsdproxy.name={appName}`
  - `tsdproxy.port.1=80/http:80/http` (headscale/plain-HTTP form; the `80/http` upstream matches Once's port-80 assumption)
  - `tsdproxy.ephemeral=true`
- **Retrofit on enable (all-or-nothing in v1)**: `once tailscale enable` performs a zero-downtime rolling recreation of every running app container to inject the labels (Docker labels are immutable on running containers), reusing the existing latest-container-kept / `removeContainersExcept` deploy path. All subsequent deploys get the labels automatically while Tailscale is enabled.
- **Retrofit on disable**: `once tailscale disable` performs the same roll to strip the `tsdproxy.*` labels. Per-app opt-out is out of scope for v1.
- These labels coexist with the existing `once`/kamal-proxy labels — an app stays reachable publicly, on localhost, and on the tailnet simultaneously.
- **Ephemeral cleanup**: because nodes are ephemeral, deleting an app takes its node offline and Tailscale/headscale auto-removes it, freeing the Magic DNS name. Quick restarts retain identity via the persisted `once-tsdproxy-data` state.

## Acceptance criteria

- [ ] App containers created while Tailscale is enabled carry the four `tsdproxy.*` labels above
- [ ] `once tailscale enable` rolls all running apps (zero-downtime) to add the labels; `disable` rolls them to remove the labels
- [ ] Existing `once`/kamal-proxy labels remain intact alongside the `tsdproxy.*` labels
- [ ] Unit test covers label generation from application settings (Tailscale on vs off)
- [ ] Integration test (slice 01 harness): deploy `whoami`, enable Tailscale, and assert the node registers (`headscale nodes list`); one end-to-end smoke boots a `tailscale` client container joined to the same headscale and curls the app via its Magic DNS name, asserting the echoed hostname
- [ ] Integration test: deleting the app takes its ephemeral node offline and frees the name

## Blocked by

- `.scratch/tailscale-integration/issues/02-tailscale-enable-disable-tsdproxy-container.md`
