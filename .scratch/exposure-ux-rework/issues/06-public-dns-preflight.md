# 06 - Public DNS preflight

**What to build:** Public deploys tell the user whether their FQDN will actually work, without ever blocking. A warn-only preflight resolves the host through an injectable resolver function (the spec's one new seam): if it does not resolve, the deploy proceeds with a warning that TLS issuance will fail until a DNS record points at this server; if it resolves, the resolved address is printed for eyeball verification. No external IP-echo dependency, no reachability probing, never a blocked deploy.

**Blocked by:** 01 - Exposure concept with local/public semantics.

**Status:** done

- [x] Public deploy with an unresolvable host succeeds and prints the TLS-will-fail warning
- [x] Public deploy with a resolvable host prints the resolved address
- [x] Local and tailnet deploys run no preflight
- [x] Tests force both outcomes through the injected resolver, no real DNS
