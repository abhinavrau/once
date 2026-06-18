Status: needs-info

# SaaS smoke pass against the real Tailscale control plane

## Parent

- PRD: `.scratch/tailscale-integration/PRD.md`
- ADR: `docs/adr/0001-tailscale-integration.md`

## What to build

A small, manually-run smoke suite covering the scenarios only the real Tailscale SaaS control plane can prove — everything headscale cannot exercise. Runs against the `tailscale-saas` control-plane backend of the slice 01 harness; **skipped** unless `ONCE_TEST_TS_OAUTH_CLIENT_ID` / `ONCE_TEST_TS_OAUTH_CLIENT_SECRET` are present. Intended to be run manually before releases.

Scenarios:
- OAuth client-credential authentication path (headscale only exercises the authkey seam).
- Real `ts.net` FQDN registration and HTTPS certificate provisioning (generous timeout — first-time cert issuance can take a minute+).
- Funnel public reachability: curl the funneled app's public URL from the test host (proves the whole data path; no tailnet client container needed).
- Ephemeral node auto-cleanup after app deletion (SaaS-side removal behavior headscale may not replicate exactly).
- SaaS test nodes use unique per-run name suffixes to avoid Magic DNS collisions between concurrent/aborted runs; ephemeral registration keeps the maintainer tailnet self-cleaning.

## Open questions (why this is `needs-info`)

Maintainer decisions needed before this is agent-ready:
- Which Tailscale account/tailnet hosts the smoke run, and how are `ONCE_TEST_TS_OAUTH_*` provisioned/stored? (Likely human-only, never in CI secrets.)
- Is the tailnet ACL configured to grant the `funnel` node attribute (required for the Funnel reachability check)?
- Should this stay a fully manual `make` target, or run in a gated/scheduled pipeline?

## Acceptance criteria

- [ ] Maintainer answers the open questions above (credential provisioning, ACL/funnel attribute, run mechanism)
- [ ] Suite runs against the `tailscale-saas` backend and is skipped cleanly when OAuth env vars are absent
- [ ] Asserts OAuth auth, real `ts.net` FQDN + HTTPS cert issuance (generous timeout), Funnel public reachability via curl, and ephemeral cleanup after delete
- [ ] Uses unique per-run name suffixes and ephemeral registration to keep the tailnet collision-free and self-cleaning

## Blocked by

- `.scratch/tailscale-integration/issues/04-lookup-api-status-list.md`
- `.scratch/tailscale-integration/issues/06-funnel-toggle.md`
- `.scratch/tailscale-integration/issues/07-funnel-auto-expiry-daemon.md`
