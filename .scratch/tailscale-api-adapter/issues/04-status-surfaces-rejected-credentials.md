# 04 - Status surfaces rejected credentials up front

**What to build:** `once tailscale status` tells the operator their OAuth
credentials were revoked, instead of misdirecting them to container logs.

Before the adapter rework, status exchanged the OAuth token eagerly and failed
deterministically with the "rejected the OAuth credentials" error. Lazy
per-call tokens moved that exchange inside the per-service cloud-check branch,
so with revoked credentials and a docktail that cannot advertise, status exits
zero printing "not advertised (check the once-docktail container logs)" for
every service - the credential failure is never mentioned. With mixed services
the error also surfaces mid-list instead of up front.

Restore the up-front failure: when the SaaS path is active (not the control
seam), a credential problem is reported once, before the per-service listing,
and status does not exit zero. The SaaS-skip branch point itself stays where
it is (settled in the mode-daemon feature).

**Blocked by:** None - can start immediately.

**Status:** done

- [x] With revoked credentials, status reports the credential error before any
      per-service output and exits non-zero, unit-tested through the fake
- [x] On the control seam, status behavior is unchanged
- [x] Healthy-credential output is unchanged
- [x] `make test` and `make lint` green

## Comments

**2026-07-30** - branch `tailscale-api-adapter-04-status-surfaces-rejected-credentials`

`serviceStatuses` now proves the credentials once, inside the existing
`cloudChecked := !controlSeamActive()` guard and before the per-service loop,
so the ADR-0010 branch point is untouched and the control seam still makes no
cloud call at all (asserted by the unchanged control-seam test).

The adapter exchanges the OAuth token lazily on its first call, and
`tailscaleAPI` has only `Call`, so forcing that exchange means making a call.
The precise fit would be a token-only seam method (e.g. `Authenticate(ctx)`)
on `tailscaleAPI`, which would let status prove the credentials without
touching the tailnet at all; `tailscale_api.go` was out of scope here, so
`checkOAuthCredentials` uses the closest existing call - the
`GET /tailnet/-/devices` read `validateHostInTailnet` already sends, needing
no scope beyond the one the per-service checks need. Worth folding into the
seam if a later ticket reopens the adapter. The path stays inline per
ADR-0011 ("paths ... stay in the flows").

Only a rejected credential is fatal there: 401 and 403 (403 naming the
devices scope, as the enable-time probe does), everything else left to the
per-service checks, so a rate-limited or briefly unwell tailnet cannot turn a
working listing into a hard failure blamed on the operator's credentials.
That boundary came out of the code review, which flagged the first cut's
blanket non-200 abort.

Healthy output is unchanged; the extra call shows up only in the call-order
assertion, which now also pins the check as running first. Also moved this
test file's helpers under a `// Helpers` section - commit 091a683's
convention sweep had missed it.

- 2026-07-30 (coordinator): merged into arch-review-wave-3-tickets at fdb5466. go vet, make test, make lint, and the full integration suite (498s) green on the merged tree. Done.
