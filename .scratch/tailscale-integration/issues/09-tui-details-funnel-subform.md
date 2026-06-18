Status: ready-for-agent

# TUI app details URLs + Funnel sub-form

## Parent

- PRD: `.scratch/tailscale-integration/PRD.md`
- ADR: `docs/adr/0001-tailscale-integration.md`

## What to build

Surface tailnet URLs and Funnel control in the per-app TUI, reading FQDN/status from the lookup API client and toggling Funnel via the existing commands.

- **App Details viewport** (`d` shortcut) dynamically shows, when Tailscale is enabled:
  - `Public/Local URL`: current public/localhost URL (e.g. `http://books.localhost`).
  - `Tailnet URL`: the private Magic DNS URL (e.g. `https://writebook.<tailnet>.ts.net`), sourced from the slice 04 lookup client.
  - `Funnel Status`: `Inactive`, or `Active (Expires in Xm)` computed from `FunnelExpiresAt`.
- **App Settings menu** (`s` shortcut): add option `7. Tailscale Funnel`, opening a sub-form to toggle Funnel and specify duration (default `10m`, max `24h`), wired to the slice 06 enable/disable flow.

## Acceptance criteria

- [ ] The details card (`d`) shows Public/Local URL, Tailnet URL, and Funnel Status when Tailscale is enabled, and omits the tailnet rows when it isn't
- [ ] Funnel Status renders `Active (Expires in Xm)` with the remaining time derived from `FunnelExpiresAt`, else `Inactive`
- [ ] The settings popup (`s`) shows `7. Tailscale Funnel`, opening a sub-form with a toggle + duration field (default 10m, capped at 24h)
- [ ] Submitting the sub-form enables/disables Funnel via the existing command code path
- [ ] Tailnet URL is read from the lookup API client (slice 04), not hard-derived

## Blocked by

- `.scratch/tailscale-integration/issues/04-lookup-api-status-list.md`
- `.scratch/tailscale-integration/issues/06-funnel-toggle.md`
- `.scratch/tailscale-integration/issues/07-funnel-auto-expiry-daemon.md`
