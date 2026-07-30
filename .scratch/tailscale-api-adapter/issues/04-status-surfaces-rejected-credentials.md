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

**Status:** ready-for-agent

- [ ] With revoked credentials, status reports the credential error before any
      per-service output and exits non-zero, unit-tested through the fake
- [ ] On the control seam, status behavior is unchanged
- [ ] Healthy-credential output is unchanged
- [ ] `make test` and `make lint` green
