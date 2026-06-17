Status: ready-for-agent

# `once teardown` full cleanup of Tailscale system containers + data volume

## Parent

- PRD: `.scratch/tailscale-integration/PRD.md`
- ADR: `docs/adr/0001-tailscale-integration.md`

## What to build

Extend the existing `once teardown` (full-cleanup) path to also remove the Tailscale system resources. This differs deliberately from `once tailscale disable`, which keeps the data volume so node identities survive a re-enable.

- `once teardown` additionally removes the `once-tsdproxy` and `once-admin` containers and **deletes** the `once-tsdproxy-data` volume.
- Teardown remains safe/idempotent when Tailscale was never enabled (no such containers/volume present).

## Acceptance criteria

- [ ] `once teardown` removes the `once-tsdproxy` and `once-admin` containers when present
- [ ] `once teardown` deletes the `once-tsdproxy-data` volume (unlike `once tailscale disable`, which retains it)
- [ ] `once teardown` is a no-op for the Tailscale resources when Tailscale was never enabled
- [ ] Integration test (slice 01 harness): after enable + teardown, none of the tsdproxy/admin containers or the data volume remain

## Blocked by

- `.scratch/tailscale-integration/issues/02-tailscale-enable-disable-tsdproxy-container.md`
- `.scratch/tailscale-integration/issues/05-admin-socket-nginx-container.md`
