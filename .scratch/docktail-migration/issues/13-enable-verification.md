# 13 - Enable verification: advertised and approved

**What to build:** After booting the Docktail topology and rolling apps, `once tailscale enable` verifies each expected service (one per exposed Application plus the Once Admin Web App) is actually reachable-in-principle: advertised according to the local daemon's serve status, and approved according to the Tailscale cloud API (authoritative, since local state can lag). A service stuck pending approval is surfaced explicitly with a pointer to autoApprovers so "enabled but unreachable" can never happen silently (user story 13). This replaces the old log-scanning registration verification.

On the headscale control seam, cloud approval checks are skipped (Services do not exist on headscale); only local advertisement is checked.

**Blocked by:** 12 (core swap).

**Status:** ready-for-agent

- [ ] Enable reports success only after every expected service is advertised (local daemon) and approved (cloud API on Tailscale SaaS)
- [ ] A pending-approval service produces a clear message naming the service and pointing at autoApprovers
- [ ] Cloud API requests are asserted byte-compatible in httptest unit tests; local-daemon states are driven through the fake from ticket 10
- [ ] No log scanning remains in the verification path
- [ ] Lint and unit tests pass
