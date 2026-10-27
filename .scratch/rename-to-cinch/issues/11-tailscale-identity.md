# 11: Tailscale identity says cinch

**What to build:** Enabling Tailscale uses cinch names throughout: the default tag `tag:cinch` (and bare `cinch` in the tag field), the `cinch-tailscale` sidecar, the `svc:cinch-probe` probe Service and its credential-probe text, and the policy snippets and screens that show them (SPEC.md, Stored identity, Tailscale rows).

**Blocked by:** 10 (Docker and host identity say cinch)

**Status:** ready-for-agent

- [ ] Every Tailscale row of the SPEC.md Stored identity table is applied
- [ ] The policy merge, snippet and screen tests assert `tag:cinch`
- [ ] `make build`, `make test`, `make lint` pass
