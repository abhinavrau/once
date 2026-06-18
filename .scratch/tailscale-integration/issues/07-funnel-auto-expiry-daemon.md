Status: ready-for-agent

# Funnel auto-expiry in the background daemon

## Parent

- PRD: `.scratch/tailscale-integration/PRD.md`
- ADR: `docs/adr/0001-tailscale-integration.md`

## What to build

Make the background daemon automatically close an expired Funnel, so a forgotten Funnel can't leave an app public indefinitely.

- The daemon's loop wakes at the **earlier** of its regular 5-minute tick or the soonest `FunnelExpiresAt` across all apps, so a 10-minute Funnel closes at 10 minutes (not up to 15).
- On expiry, the daemon clears `FunnelExpiresAt`, removes the `tailscale_funnel` option from the container labels, and recreates the container to close the Funnel — the same atomic recreation used by the manual toggle.
- Because expiry timestamps are persisted in the app `once` label / state, teardown survives daemon restarts (a Funnel whose expiry already passed while the daemon was down is torn down on next start).
- Optionally record a `LastFunnelTeardown OperationResult` in state, matching the existing `LastBackup`/`LastUpdate` pattern.

## Acceptance criteria

- [ ] The daemon computes its next wake as `min(regular 5-min tick, soonest FunnelExpiresAt)`
- [ ] On expiry the daemon removes the `tailscale_funnel` option, clears `FunnelExpiresAt`, and recreates the container
- [ ] A Funnel whose expiry passed during downtime is torn down on the next daemon start
- [ ] Unit tests set `FunnelExpiresAt` in the past/future and verify the expiration checker correctly identifies whether teardown is due (mirroring `runner_test.go` prior art)
- [ ] Unit test verifies the wake-interval calculation picks the soonest expiry when sooner than the regular tick

## Blocked by

- `.scratch/tailscale-integration/issues/06-funnel-toggle.md`
