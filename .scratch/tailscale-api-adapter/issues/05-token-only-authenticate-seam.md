# 05 - Token-only credential check on the SaaS seam

**What to build:** `once tailscale status` proves the operator's OAuth
credentials by exchanging the token alone, instead of borrowing the tailnet
devices read as a stand-in - so a credential check never touches tailnet data
it does not need and cannot fail for tailnet-shaped reasons.

Ticket 04 wanted exactly this and recorded the compromise: the seam exposes
only `Call`, so status forces the lazy token exchange by issuing the devices
read. Add the token-only operation to the seam (the adapter already has the
exchange; this exposes it as a first-class call), point status's up-front
credential check at it, and keep the fake adapter able to can and record it
like any other call. The status error boundary from ticket 04 (only rejected
credentials are fatal up front; 429/5xx stay the per-service checks'
business) must survive unchanged.

**Blocked by:** None - can start immediately (tickets 03 and 04 are done).

**Status:** in-review

- [x] The seam offers a token-only credential operation; status's up-front
      check uses it and no longer issues the devices read
- [x] Rejected credentials still abort before per-service output with the
      same wording; 429/5xx still do not
- [x] The fake adapter cans, records, and error-injects the new operation
      like any other
- [x] Control-seam behavior unchanged (still zero cloud calls)
- [x] `make test` and `make lint` green

## Comments

**2026-07-30** - branch `tailscale-api-adapter-05-token-only-authenticate-seam`

Added `Authenticate(ctx) error` to `tailscaleAPI`. The production adapter
exchanges the OAuth token and discards it; `checkOAuthCredentials` in
`tailscale_status.go` calls this instead of `Call(GET, /tailnet/-/devices)`,
so status no longer touches tailnet data at all before the per-service loop.

The boundary needed two sentinels, not one, once the check stopped reading a
status code off a devices response: `ErrOAuthCredentialsRejected` (400/401/403
from the token endpoint, unchanged wording from `tokenExchangeError`) and a
new `ErrOAuthTokenEndpointUnavailable` (429/5xx, "not rejected, try again").
`checkOAuthCredentials` swallows only the latter and forwards everything else
- rejection or anything unproven (a network failure reaching the token
endpoint, an unparseable response) - as fatal, forwarded verbatim rather than
reworded. A first draft swallowed anything that wasn't a rejection, which
silently dropped network failures too; code review caught it, second draft
narrows the tolerance to just the named "not rejected" sentinel.

Accepted regression, flagged by review and left as-is because it's what a
token-only check can no longer do by construction: the old devices-read stand-in
could return 403 for a client missing the devices scope, and
`checkOAuthCredentials` named that specifically ("the OAuth client is missing
the devices scope..."). A token exchange can't observe a per-endpoint scope,
so that case is gone from the up-front check - a client with valid but
under-scoped credentials no longer aborts before any service is reported, and
if nothing is advertised the failure never surfaces (the same shape of bug
ticket 04 fixed for revoked credentials, now reopened for this one narrower
cause). This is the tradeoff the ticket's "cannot fail for tailnet-shaped
reasons" describes, not an oversight - worth a follow-up if it turns out to
bite in practice.

The fake reuses `Call`'s `apiRequest{method, path}` bookkeeping for
`Authenticate` under a sentinel method and empty path, so it is canned,
recorded, and error-injected through the same `reply`/`fail`/`requested` calls
- "like any other" per the ticket, not a bespoke mechanism. `Call` and
`Authenticate` share a `record` helper now (code review flagged the
duplication in the first draft).

ADR-0011 updated: the seam description said "a one-method interface", now
describes both `Call` and `Authenticate`.
