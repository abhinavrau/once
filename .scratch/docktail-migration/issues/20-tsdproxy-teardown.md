# 20 - TSDProxy teardown and clean-break upgrade message

**What to build:** The contract step of the clean break:

- Delete the TSDProxy module entirely: pinned image constant, YAML config template and copy-in, lookup API client and its loopback port binding, registration-error log scanning, the tsdproxy labels helper, and the tsdproxy-era unit and headscale-registration tests that go with them.
- An upgraded install with a running `once-tsdproxy` container gets a clear message telling the user to re-run `once tailscale enable`; Once does not migrate or remove the old container automatically.
- Destroy (full namespace teardown) removes both old and new Tailscale containers and volumes.
- CONTEXT.md's TSDProxy glossary entry is replaced by a Docktail entry (avoid: TSDProxy, Tailscale Docker Proxy) plus a Tailscale Service entry.

**Blocked by:** 13, 14, 15, 18 (every remaining consumer of the tsdproxy module must be off it first).

**Status:** done

- [x] No tsdproxy code, config template, API client, log scanning, or labels helper remains; carried-over tests (settings marshalling, tag normalization) survive
- [x] With an old once-tsdproxy container present, tailscale commands surface the re-run message and never manage that container
- [x] Teardown removes old-name and new-name Tailscale containers and volumes; integration test covers both
- [x] CONTEXT.md glossary updated: Docktail and Tailscale Service entries in, TSDProxy entry replaced
- [x] Lint, unit, and integration tests pass

## Comments

2026-07-15: Most of the module (image constant, config template/copy-in, lookup API
client, loopback port) had already been deleted by the earlier tickets; this ticket
removed the last dead remnants - `tsdproxyRegistrationFailures`, `tsdproxyLabels`, and
`scanRegistrationError` plus their tests (`TestScanRegistrationError`,
`TestTSDProxyLabels`). Carried-over tests (settings round-trip incl. legacy apiKey drop,
tag normalization) survive. The upgrade message is `Tailscale.LegacyTSDProxyWarning`,
printed by every `once tailscale` subcommand via the `withLegacyTSDProxyWarning` wrapper;
nothing but `Destroy` (full teardown) touches the legacy container/volume, verified by
grep - the sweep there predates this ticket. `TestTeardownRemovesTailscaleSystemResources`
now plants a legacy `once-tsdproxy` container and `-tsdproxy-data` volume and asserts the
warning fires and teardown removes them alongside the docktail/sidecar resources. The
CONTEXT.md glossary was already in its target state (Docktail + Tailscale Service entries,
TSDProxy only in the Avoid line). Also deleted the stale root-level
TAILSCALE-FEATURE-PLAN.md, which still described the TSDProxy architecture as the design
of record (ADR-0001/0002 carry the history). Lint, unit, and full integration suites green.
