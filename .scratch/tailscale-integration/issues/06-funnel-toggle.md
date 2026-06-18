Status: ready-for-agent

# Funnel toggle: `FunnelExpiresAt` setting + label generation + `once tailscale funnel enable/disable`

## Parent

- PRD: `.scratch/tailscale-integration/PRD.md`
- ADR: `docs/adr/0001-tailscale-integration.md`

## What to build

Let an admin grant temporary public access to a single app via Tailscale Funnel by toggling the container's labels. (Automatic expiry is slice 07; this slice does the manual toggle and the state plumbing.)

- Add `FunnelExpiresAt *time.Time` to the `ApplicationSettings` struct, persisted in the app container's `once` label. The expiry and the funnel label must be written atomically in the **same** container recreation, so the two can never drift apart.
- Funnel is enabled by adding the `tailscale_funnel` option to the port label (e.g. `tsdproxy.port.1=443/https:80/http, tailscale_funnel`); disabled by removing that option.
- New funnel commands nested under `tailscale` (mirroring the upstream `tailscale funnel` CLI):
  - `once tailscale funnel enable <app-name> [--duration 10m]` — computes the expiry, sets `FunnelExpiresAt`, applies labels, recreates the container. Default `10m`, **max `24h`** (reject longer).
  - `once tailscale funnel disable <app-name>` — removes the funnel option, clears `FunnelExpiresAt`, recreates the container (revoke immediately).
- **Daemon required**: enabling a Funnel fails with a clear error if the background service is not installed and running — without the daemon, automatic teardown (slice 07) could never happen and the app would stay public indefinitely.
- **Surface activation errors**: Funnel only works if the tailnet ACL grants the `funnel` node attribute (Once cannot manage ACLs). If TSDProxy fails to activate the Funnel, surface the error rather than reporting it active.
- Funnel verification is **unit-level only** — the headscale suite must never set `tailscale_funnel` (it would error the proxy). Live verification is the SaaS smoke pass (slice 11).

## Acceptance criteria

- [ ] `ApplicationSettings` has `FunnelExpiresAt *time.Time`, serialized in the `once` label
- [ ] Enabling/disabling Funnel writes the label option and `FunnelExpiresAt` in a single atomic container recreation
- [ ] `once tailscale funnel enable <app> [--duration]` defaults to 10m and rejects durations over 24h
- [ ] `once tailscale funnel disable <app>` removes the option and clears `FunnelExpiresAt` immediately
- [ ] Enabling Funnel without a running daemon fails with a clear error
- [ ] TSDProxy funnel-activation failures are surfaced, not reported as active
- [ ] Unit tests cover funnel label generation (on/off) and the duration default/cap; no headscale test sets `tailscale_funnel`

## Blocked by

- `.scratch/tailscale-integration/issues/03-app-tsdproxy-labels-retrofit-roll.md`
