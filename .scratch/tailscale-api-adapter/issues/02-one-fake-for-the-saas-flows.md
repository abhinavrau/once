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

**Status:** done

- [x] One fake adapter serves every SaaS-touching flow test; the three hand-rolled flow servers are deleted
- [x] Canned responses are parsed by the production code paths, not by test-local parsing
- [x] Exact-byte assertions on docktail-compatible payloads and request paths are preserved through the fake's recordings
- [x] An unexpected request fails the test rather than being silently ignored
- [x] Flow tests no longer assert token or bearer mechanics; a first-call failure aborts the probe without further calls
- [x] The only remaining httptest server in the Tailscale tests exercises the HTTP adapter
- [x] All unit tests pass

## Comments

### 2026-07-30 - tailscale-api-adapter-02-one-fake-for-the-saas-flows

`fakeTailscaleAPI` in `internal/docker/tailscale_api_fake_test.go` now serves
all five SaaS flows: canned status/body per method-and-path (parsed by the
production code, never test-locally), recorded calls with payload bytes,
per-request error injection, and an unexpected call reported against the flow
that made it. The probe, approval, and devices servers are gone; `apiAgainst`
is used only by `tailscale_api_test.go`, the sole remaining httptest server.
Token and bearer assertions moved out of the flows (they are already covered
there, including `grant_type` and the 401 wording); the rejected-credentials
probe test is now a failing first call with exactly one recorded call. Payload
assertions got stricter on the way: the key request is compared byte-for-byte
against `sidecarKeyPayload` instead of through an unmarshalled struct. Mutation
checks confirmed the migration is not vacuous - changing a canned key id fails
the success test with `unexpected Tailscale API call DELETE
/tailnet/-/keys/kZZZ`, and disabling the fake's reporting reddens its
self-tests. `make test` green, `make lint` clean.

Left for a production-side ticket (scope guard): the comment at
`internal/docker/tailscale_validate.go:99` still says tests "can use an httptest
server and the canned-JSON fake" - the preflight tests now use only the fake.

- 2026-07-30 (coordinator): merged into arch-review-wave-2-tickets at 79e1eaf. go vet, make test, make lint, and the full integration suite (489s) green on the merged tree. Done.
