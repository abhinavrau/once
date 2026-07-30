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

**Status:** ready-for-agent

- [ ] A SaaS endpoint that accepts the connection but never responds fails the
      enable within a bounded time, unit-tested against the fake or a stalled
      httptest server
- [ ] A devices list larger than the body bound produces an error naming the
      oversized response, not a JSON parse error; a realistic large tailnet
      passes
- [ ] A 429 or 5xx from the token endpoint is not reported as rejected
      credentials, with wording tests
- [ ] Token exchanges per enable run are reduced to one, or the multiplicity
      is documented in this ticket as accepted
- [ ] `make test` and `make lint` green
