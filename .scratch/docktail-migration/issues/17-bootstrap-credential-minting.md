# 17 - Bootstrap credential minting (optional path)

**What to build:** The enable flow optionally accepts one `oauth_keys`-scoped bootstrap credential and mints the runtime and policy-write OAuth clients via the Tailscale API, shrinking the user's admin-console work to creating a single credential (user story 10). The bootstrap and policy credentials are used once and never stored; only the minimal-scope runtime credential is persisted in the `once` label (user story 11).

**Gate first:** confirm Tailscale's scope attenuation rules - whether an API-created OAuth client's scopes must be a subset of its creator's. If minting is unattenuated (or attenuation makes the flow impossible), record the finding in this issue's Comments and keep the per-client prompt as the default; this path stays strictly optional either way.

**Blocked by:** 11 (probe defines runtime scopes), 16 (policy client consumer).

**Status:** ready-for-agent

- [ ] Scope attenuation rules confirmed and recorded before the path is exposed
- [ ] Enable accepts a single bootstrap credential and mints runtime + policy clients with minimal scopes
- [ ] Bootstrap and policy credentials are never persisted; only the runtime credential lands in the `once` label
- [ ] Minting requests asserted byte-compatible in httptest unit tests, including failure paths (insufficient scope, minting rejected)
- [ ] The per-client prompt path remains the default and fully functional
- [ ] Lint and unit tests pass
