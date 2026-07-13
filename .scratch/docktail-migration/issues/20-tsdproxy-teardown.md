# 20 - TSDProxy teardown and clean-break upgrade message

**What to build:** The contract step of the clean break:

- Delete the TSDProxy module entirely: pinned image constant, YAML config template and copy-in, lookup API client and its loopback port binding, registration-error log scanning, the tsdproxy labels helper, and the tsdproxy-era unit and headscale-registration tests that go with them.
- An upgraded install with a running `once-tsdproxy` container gets a clear message telling the user to re-run `once tailscale enable`; Once does not migrate or remove the old container automatically.
- Destroy (full namespace teardown) removes both old and new Tailscale containers and volumes.
- CONTEXT.md's TSDProxy glossary entry is replaced by a Docktail entry (avoid: TSDProxy, Tailscale Docker Proxy) plus a Tailscale Service entry.

**Blocked by:** 13, 14, 15, 18 (every remaining consumer of the tsdproxy module must be off it first).

**Status:** ready-for-agent

- [ ] No tsdproxy code, config template, API client, log scanning, or labels helper remains; carried-over tests (settings marshalling, tag normalization) survive
- [ ] With an old once-tsdproxy container present, tailscale commands surface the re-run message and never manage that container
- [ ] Teardown removes old-name and new-name Tailscale containers and volumes; integration test covers both
- [ ] CONTEXT.md glossary updated: Docktail and Tailscale Service entries in, TSDProxy entry replaced
- [ ] Lint, unit, and integration tests pass
