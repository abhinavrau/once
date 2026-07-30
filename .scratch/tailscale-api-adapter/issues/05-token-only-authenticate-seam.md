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

**Status:** ready-for-agent

- [ ] The seam offers a token-only credential operation; status's up-front
      check uses it and no longer issues the devices read
- [ ] Rejected credentials still abort before per-service output with the
      same wording; 429/5xx still do not
- [ ] The fake adapter cans, records, and error-injects the new operation
      like any other
- [ ] Control-seam behavior unchanged (still zero cloud calls)
- [ ] `make test` and `make lint` green
