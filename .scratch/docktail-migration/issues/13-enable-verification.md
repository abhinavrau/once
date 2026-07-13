# 13 - Enable verification: advertised and approved

**What to build:** After booting the Docktail topology and rolling apps, `once tailscale enable` verifies each expected service (one per exposed Application plus the Once Admin Web App) is actually reachable-in-principle: advertised according to the local daemon's serve status, and approved according to the Tailscale cloud API (authoritative, since local state can lag). A service stuck pending approval is surfaced explicitly with a pointer to autoApprovers so "enabled but unreachable" can never happen silently (user story 13). This replaces the old log-scanning registration verification.

On the headscale control seam, cloud approval checks are skipped (Services do not exist on headscale); only local advertisement is checked.

**Blocked by:** 12 (core swap).

**Status:** done

- [x] Enable reports success only after every expected service is advertised (local daemon) and approved (cloud API on Tailscale SaaS)
- [x] A pending-approval service produces a clear message naming the service and pointing at autoApprovers
- [x] Cloud API requests are asserted byte-compatible in httptest unit tests; local-daemon states are driven through the fake from ticket 10
- [x] No log scanning remains in the verification path
- [x] Lint and unit tests pass

## Comments

2026-07-13: Implemented as `Tailscale.VerifyServices` (internal/docker/tailscale_verify.go),
called at the end of `Namespace.EnableTailscale` after once-admin boots. Expected
services are one per running exposed Application plus once-admin (stopped apps are
never advertised by Docktail, so they can't be verified). Two layers, both bounded
by a 90s poll: local advertisement via the ticket-10 daemon seam's ServeStatus, then
cloud approval via `GET /tailnet/-/services/svc:<name>/devices` - byte-compatible
with Docktail's listServiceHosts, asserted in httptest tests. approvalLevel values
(not-approved / approved:auto / approved:manual) confirmed against Tailscale's
OpenAPI spec. A not-approved host fails fast (waiting can't fix it) with a message
naming the service and pointing at autoApprovers.services; a service the control
plane never learns of fails at the deadline pointing at the once-docktail logs.

Deviation from the ticket: on the headscale control seam verification is skipped
ENTIRELY, not just the cloud half. Verified empirically (throwaway integration
probe): docktail's `tailscale serve --service --https` fails against headscale
with "error enabling https feature: 404" - the control-plane HTTPS cert machinery
issue 03 flagged - so even local advertisement can never succeed there. The
registration log-scanning path was already dead code (scanRegistrationError has no
callers); it stays for ticket 20's teardown. No TUI form fields changed, so the
ui_test.go driver needed no update. Lint, unit, and integration suites green.
