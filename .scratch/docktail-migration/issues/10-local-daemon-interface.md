# 10 - Local tailscaled daemon interface

**What to build:** The single new seam from the PRD: a small interface through which Once reads everything it needs from a local tailscaled - the Magic DNS suffix, serve/services status (which services are advertised), and funnel status. Implemented with `tailscale ... --json` invocations, either by `docker exec` into the Once-managed sidecar or via the host CLI in host mode. Ships with a fake implementation driven by canned `status --json` / serve-status JSON so all later tickets can unit-test against it (Seam 2 in the PRD's Testing Decisions).

This is pure prefactoring: no user-visible behavior changes yet. "Make the change easy, then make the easy change."

**Blocked by:** None - can start immediately.

**Status:** ready-for-agent

- [ ] An interface exists exposing: Magic DNS suffix, advertised services (serve status), and funnel activation state
- [ ] A `docker exec` implementation (sidecar mode) and a host-CLI implementation (host mode) exist behind it
- [ ] A fake implementation accepts canned JSON fixtures and is usable from unit tests
- [ ] Unit tests cover parsing of suffix, advertised-services, and funnel-status JSON, including error/absent cases
- [ ] Lint and unit tests pass; no existing behavior changes
