# 01 - The Tailscale SaaS seam is a type, not a string

**What to build:** Every flow that reaches the Tailscale SaaS control-plane API
(enable preflight, credential probe, sidecar key mint, service verify, service
status) does so through one unexported interface with a single authenticated
Call method, implemented by an HTTP adapter that owns the production base URL,
the OAuth client-credentials exchange (lazy, cached for the instance's
lifetime, constructed once per operation), the bearer header, and the bounded
response read. The baseURL string that today threads through 7 signatures
directly and 5 more in grouped parameter lists disappears, along with the
hand-threaded bearer token; the production URL appears in exactly one place,
inside the adapter. Payload and path construction and status-to-error wording
stay in the flows: the requests docktail also sends must remain byte-identical,
and error wording is flow-specific by decision.

The adapter performs no logging of any kind and uses the wrapped default
transport, so ADR-0005's metadata-only verbose logging keeps holding by
construction. Every control-seam branch stays exactly where it is - the
parallel tailscale-mode-daemon feature owns that seam.

Existing flow tests stay green in this ticket by constructing the real adapter
against the httptest servers they already own (their shared helper functions
centralize the construction); replacing those servers with a fake is ticket 02.

**Blocked by:** None - can start immediately.

**Status:** done

- [x] One unexported single-method interface is the only path from the flows to the Tailscale SaaS API; no raw http client use remains in the flows
- [x] No function signature carries a base URL or a bearer token; the production base URL exists in exactly one place
- [x] The OAuth exchange happens lazily inside the adapter, at most once per operation, and a credential rejection keeps its exact current wording and still fails the probe before any auth key is minted
- [x] Adapter unit tests (httptest, in-CI stand-in for the SaaS OAuth path) cover: successful exchange, rejection wording, a token response with no access token, the bearer header on subsequent calls, token reuse across calls within one instance, the bounded response-body read, and network-error wrapping
- [x] Docktail-compatible request payloads and paths are still built by the flows and the existing exact-byte test assertions pass unchanged
- [x] The adapter emits no log lines; verbose logging of its requests still comes solely from the metadata-only transport (ADR-0005)
- [x] All control-seam branch points are untouched
- [x] All existing unit and flow tests pass

## Comments

### 2026-07-29 - tailscale-api-01-saas-seam

Worktree: /home/arau/code/once/.claude/worktrees/agent-a9d3ba8030728541f

Added `tailscaleAPI` (one authenticated `Call`) and `httpTailscaleAPI` in
`internal/docker/tailscale_api.go`: the adapter owns the single production base
URL, the lazy per-instance OAuth exchange, the bearer header, and one bounded
read shared by the exchange and every call; it logs nothing and rides the
wrapped default transport. All 12 signatures lost their `baseURL`/token, the
five flows now take the seam, and paths, payloads, and status-to-error wording
stayed put - the docktail byte-compat assertions pass unchanged. Two knock-on
effects of the lazy exchange, both inherent to PRD decision 3: `waitForApproved`
now announces its phase before the exchange, and `ServiceStatuses` no longer
fails eagerly on bad credentials when nothing is advertised (it reports the
local not-advertised truth instead; a credential failure still surfaces
verbatim as soon as an advertised service is checked). Code comments cite
ADR-0011, which lands with the coordinator's uncommitted ADR file.

2026-07-29 (coordinator): merged into arch-review-frontier-tickets in ff51122; unit tests, lint, and the full integration suite green on the merged branch (437s, all pass). Status set to done.
