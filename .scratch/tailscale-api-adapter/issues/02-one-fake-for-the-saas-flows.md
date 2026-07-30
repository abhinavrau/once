# 02 - One fake replaces three hand-rolled servers

**What to build:** A single fake adapter satisfies the Tailscale SaaS seam for
all flow tests, replacing the three hand-rolled httptest servers (the probe
server, the approval server, and the devices server), each of which currently
re-implements the OAuth token endpoint. The fake follows the FakeTailscaleDaemon
model: canned status and body per expected request, so canned JSON still flows
through the production parsing and cannot drift from it; recorded calls
including payload bytes, so the exact-byte docktail-compatibility assertions
survive; per-request error injection for transport failures; and an unexpected
request fails the test, as the servers do today.

Flow tests stop asserting token-exchange and bearer mechanics - those belong to
the adapter's own unit tests from ticket 01. The flow test for rejected
credentials becomes: the first call fails, the probe returns that error and
makes no further calls. After this ticket the only httptest server left in the
Tailscale tests is the one exercising the HTTP adapter itself.

**Blocked by:** 01 - The Tailscale SaaS seam is a type, not a string.

**Status:** ready-for-agent

- [ ] One fake adapter serves every SaaS-touching flow test; the three hand-rolled flow servers are deleted
- [ ] Canned responses are parsed by the production code paths, not by test-local parsing
- [ ] Exact-byte assertions on docktail-compatible payloads and request paths are preserved through the fake's recordings
- [ ] An unexpected request fails the test rather than being silently ignored
- [ ] Flow tests no longer assert token or bearer mechanics; a first-call failure aborts the probe without further calls
- [ ] The only remaining httptest server in the Tailscale tests exercises the HTTP adapter
- [ ] All unit tests pass
