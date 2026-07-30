# 03 - Harden the HTTP adapter against a misbehaving network

**What to build:** An enable or preflight that hits a stalled, throttled, or
oversized Tailscale SaaS response fails fast with an error naming the real
problem, instead of hanging forever or blaming the user's credentials.

Three defects in the HTTP adapter, found by post-merge review:

1. Every SaaS request goes through `http.DefaultClient`, which has no timeout,
   and the TUI enable path passes a background context - so a connection that
   accepts TCP but never responds (captive portal, blackholed egress) hangs the
   enable forever with no cancel path. Give the adapter its own client with a
   sane timeout; the service-verify poll budget must still govern the loop, not
   the individual call.
2. The bounded body reader silently truncates responses at 64KiB with no
   truncation detection. A tailnet of roughly 70+ devices crosses the cap and
   host-mode enable aborts with "unexpected end of JSON input" - a bogus error
   that scales in with tailnet size. Detect the truncation and either raise the
   bound or fail with an error saying the response was too large.
3. The OAuth token exchange maps every non-200, including 429 and 5xx, to
   "Tailscale rejected the OAuth credentials (...)", sending users off to
   rotate a valid secret. Rate-limit and server-error responses get their own
   wording. Related: one enable run builds up to three adapter instances, each
   with its own token cache, so the exchange runs up to three times per enable
   and makes 429s likelier - share one adapter (or one token) per run if the
   fix is cheap; otherwise record it here as accepted.

**Blocked by:** None - can start immediately (tickets 01 and 02 are done).

**Status:** done

- [x] A SaaS endpoint that accepts the connection but never responds fails the
      enable within a bounded time, unit-tested against the fake or a stalled
      httptest server
- [x] A devices list larger than the body bound produces an error naming the
      oversized response, not a JSON parse error; a realistic large tailnet
      passes
- [x] A 429 or 5xx from the token endpoint is not reported as rejected
      credentials, with wording tests
- [x] Token exchanges per enable run are reduced to one, or the multiplicity
      is documented in this ticket as accepted
- [x] `make test` and `make lint` green

## Accepted: up to three token exchanges per enable run

The multiplicity in item 3 stays. Sharing one adapter per run means changing
how the five flows construct it, which is call-site work this ticket is scoped
out of; sharing one token means a process-level cache, and that is not cheap:

- ADR-0011 and the PRD both settle adapter lifetime at one operation, and the
  type relies on it - a per-instance token needs no expiry handling and no
  locking. A process-level cache outlives the ~1h token, so it needs both, and
  a TUI session lives long enough to hit expiry. That trades a rare 429 for a
  new class of stale-token failure.
- The exchange is one small request per operation, and an enable makes at most
  three. Tailscale's OAuth rate limit is nowhere near that; the 429 wording
  added here is for a shared-IP or retry-storm case, which one fewer exchange
  would not prevent either.

Revisit if adapter lifetime ever widens beyond one operation - at that point
the token cache moves with it and expiry handling is required anyway.

## Comments

### 2026-07-30 - branch `tailscale-api-adapter-03-harden-the-http-adapter`

All three defects fixed inside `internal/docker/tailscale_api.go`; no call site,
UI, or integration file touched.

- The adapter owns its client (`newTailscaleAPIAt`), with a 30s per-request
  timeout - `http.DefaultClient` has none. Transport is left nil, so requests
  still ride `http.DefaultTransport` and stay behind main's metadata-only
  logging RoundTripper (ADR-0005); a test now swaps that transport and counts
  the trips rather than asserting the field is nil. The timeout is well under
  `serviceVerifyTimeout` (90s), the only poll budget driving API calls, so the
  budget still governs the loop; a test pins that ordering. Stalled-server
  tests cover both legs, the token exchange and the request it authorizes.
- The body bound is now 8MiB, and `readBoundedBody` reads bound+1 to detect
  truncation: an oversized body fails naming the request it answered
  ("the Tailscale API response to GET /tailnet/-/devices is larger than ...")
  instead of being parsed from truncated bytes. An 80-device list of
  realistically sized records (past the old 64KiB bound) is driven through the
  real `validateHostInTailnet` parser to prove a large tailnet passes.
- `tokenExchangeError` splits the status mapping four ways: 429 rate-limited,
  5xx server error, 400/401/403 rejected credentials (wording unchanged, ticket
  04 depends on it), anything else "unexpected response". The two non-credential
  branches say the credentials were not rejected, so nobody rotates a working
  secret over an outage.

Token-exchange multiplicity stays as recorded above. `make test` and
`PATH="$HOME/go/bin:$PATH" make lint` green (bare `make lint` cannot find
golangci-lint); integration suite not run, per the ticket's scope.

- 2026-07-30 (coordinator): merged into arch-review-wave-3-tickets at 9e439a6. go vet, make test, make lint, and the full integration suite (498s) green on the merged tree. Done.
