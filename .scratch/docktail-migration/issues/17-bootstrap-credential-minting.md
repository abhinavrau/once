# 17 - Bootstrap credential minting (optional path)

**What to build:** The enable flow optionally accepts one `oauth_keys`-scoped bootstrap credential and mints the runtime and policy-write OAuth clients via the Tailscale API, shrinking the user's admin-console work to creating a single credential (user story 10). The bootstrap and policy credentials are used once and never stored; only the minimal-scope runtime credential is persisted in the `once` label (user story 11).

**Gate first:** confirm Tailscale's scope attenuation rules - whether an API-created OAuth client's scopes must be a subset of its creator's. If minting is unattenuated (or attenuation makes the flow impossible), record the finding in this issue's Comments and keep the per-client prompt as the default; this path stays strictly optional either way.

**Blocked by:** 11 (probe defines runtime scopes), 16 (policy client consumer).

**Status:** done

- [x] Scope attenuation rules confirmed and recorded before the path is exposed
- [x] Enable accepts a single bootstrap credential and mints runtime + policy clients with minimal scopes
- [x] Bootstrap and policy credentials are never persisted; only the runtime credential lands in the `once` label
- [x] Minting requests asserted byte-compatible in httptest unit tests, including failure paths (insufficient scope, minting rejected)
- [x] The per-client prompt path remains the default and fully functional
- [x] Lint and unit tests pass

## Comments

2026-07-14: Scope attenuation gate resolved: minting is UNATTENUATED per all published Tailscale documentation. Evidence: the official OpenAPI spec (api.tailscale.com/api/v2?outputOpenapiSchema=true) documents `POST /tailnet/{tailnet}/keys` with `keyType:"client"` plus free-form `scopes`/`tags`/`description` fields, states only "OAuth Scope: `oauth_keys` grants access to create OAuth clients", and imposes no subset-of-creator rule. kb/1215 and kb/1623 restrict creation per console *user role* only (Owners/Admins: any scope); a key created with an OAuth-client-derived token is "owned by the tailnet" with no documented scope ceiling. This matches research issue 08's read that `oauth_keys` is effectively admin-equivalent. Consequence: a bootstrap client scoped to `oauth_keys` alone can mint the `auth_keys,devices:core,services` runtime client and the `policy_file` policy client. The path ships strictly optional (`--bootstrap-client-id/--bootstrap-client-secret` on the CLI), the per-client prompt stays the default, and a 403/4xx at mint time degrades with an actionable error - so if Tailscale ever adds server-side attenuation, the flow fails loud, not wrong. Wire shape follows tailscale-client-go-v2 Keys.CreateOAuthClient (`{"keyType":"client","scopes":[...],"tags":[...],"description":"..."}`); the response's `id` is the client ID and `key` is the client secret (confirmed against terraform-provider-tailscale's oauth_client resource).

2026-07-14: Implemented. internal/docker/tailscale_bootstrap.go holds TailscaleBootstrap (token exchange + MintRuntimeClient/MintPolicyClient/DeleteClient, httptest-seamed via newTailscaleBootstrap); the CLI path is `once tailscale enable --bootstrap-client-id/--bootstrap-client-secret --tag`, mutually exclusive with --client-id/--client-secret via cobra flag groups, so the per-client path stays the default. Flow order: policy offer first (fresh tailnets must define the tag in tagOwners before a tagged client can be minted - same lesson as issue 16), using a minted one-shot policy_file client that is deleted right after the write (best-effort; a failed delete prints a revoke-it-yourself pointer); then the runtime client (auth_keys, devices:core, services; tags from the same normalizer as the policy snippet) is minted and flows into the normal enable, whose probe still validates it before any container boots. The TUI form keeps the per-client path only - adding a bootstrap field would break the tab-order form drivers in integration/ui_test.go for an optional path (add it there if users ask).
