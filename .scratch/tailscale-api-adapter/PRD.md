# PRD - Turn baseURL into an adapter (tailscale-api-adapter)

## Problem

The Tailscale SaaS control-plane API has no seam. Testability was bought by
threading a `baseURL string` parameter - one production value, the constant
`https://api.tailscale.com/api/v2` - through 7 signatures that declare it
directly, plus 5 more that carry it inside grouped parameter lists along with a
bearer token. Two call sites hit `http.DefaultClient.Do` raw. Three hand-rolled
httptest servers (probe, approval, devices) each re-implement the OAuth token
endpoint to receive the redirected URL. Every flow re-runs the OAuth
client-credentials exchange itself and threads the resulting token by hand.

## Accepted design

One unexported seam in the Docker module, mirroring the TailscaleDaemon model
(the one seam in this subsystem that already works):

- `tailscaleAPI` - a single-method interface: an authenticated
  `Call(ctx, method, path, payload) (status, body, error)`. Deep: one method
  hides the production base URL, the OAuth exchange and its caching, the bearer
  header, the bounded response read, and network-error wrapping.
- `httpTailscaleAPI` - the production adapter. Holds base URL and OAuth client
  credentials, exchanges the token lazily on first Call and caches it for the
  instance's lifetime. Constructed per operation at the five flows that reach
  the SaaS today (enable preflight, credential probe, sidecar key mint, service
  verify, service status), matching today's one-token-per-operation behavior so
  no expiry handling is needed.
- `fakeTailscaleAPI` - the test adapter: canned status/body per request,
  recorded calls, error injection. Replaces all three hand-rolled servers.
  Canned JSON still flows through the production parsers, so the fake cannot
  drift.

Deliberately NOT a domain-level interface (no MintAuthKey/PutService methods):
payload and path construction stays in the flows because several requests must
remain byte-compatible with what docktail sends, and status-to-error mapping
stays in the flows because error wording is flow-specific by prior decision.
The interface carries no token and no baseURL; both disappear from all 12
signatures.

## Decisions (settled this review - do not re-argue)

1. The seam passes the deletion test: delete it and token exchange, bearer
   auth, bounded reads, and the base URL reappear in every flow. Real seam,
   two adapters.
2. Transport-level `Call`, not domain-level methods (byte-compat and
   per-flow error wording pin those to the callers).
3. OAuth exchange lives inside the http adapter, lazy, cached per instance;
   its credential-failure wording moves verbatim. Ordering preserved: bad
   credentials still fail before any key is minted.
4. Adapter lifetime is one operation; no field on the Tailscale struct.
5. Injection stays parameter-shaped: the `baseURL string` and `token`
   parameters of the private flow functions become one `tailscaleAPI`
   parameter. No package-level var.
6. ADR-0005: the adapter sits above the metadata-only logging RoundTripper
   (it uses the wrapped default transport) and performs no logging of any
   kind. It never feeds bodies, headers, or payloads to any log.
7. Multiple VPN providers are coming. The adapter stays Tailscale-specific
   and unexported; a sibling provider gets its own adapter module. No
   provider-generic interface until a second provider exists.
8. The fake lives in a test file (nothing outside the Docker module needs it).
9. SaaS OAuth cannot run in CI: the exchange is covered by httptest unit
   tests against the http adapter - the one remaining httptest server.

## Constraints

- Requests that must match docktail's real calls stay byte-compatible:
  payloads, paths (including the "-" tailnet segment), and the 404-tolerant
  delete are untouched; the adapter only transports them.
- Warnings convention is ([]string, error); frontend-specific error wording
  stays per-frontend. Neither changes here.
- Every controlSeamActive() branch stays exactly where it is.

## Cross-feature dependencies

- **tailscale-mode-daemon** (candidate 04, grilled in parallel): owns mode
  resolution, daemon binding, and the control seam. No ticket here is blocked
  by it - this feature substitutes a type where a string went and leaves the
  control-seam branches in place - but ticket 01 rewires call sites adjacent
  to lines 04 may move (the enable preflight and the sidecar boot). Whichever
  feature lands second rebases mechanically; the coordinator sequences.

## Tickets

- 01-tailscale-api-seam.md - the seam, the http adapter, the rewire
- 02-one-fake-for-the-saas-flows.md - the fake, flow-test migration, delete
  the three servers
