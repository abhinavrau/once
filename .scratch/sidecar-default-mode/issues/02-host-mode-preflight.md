# 02 - Host-mode pre-flight, four hard failures

**What to build:** Per ADR-0007, a user who passes `--mode host` learns within
seconds whether host mode can work, instead of watching the enable boot the
full topology and die 90 silent seconds later in the service-advertisement
verify. At probe time (alongside the credential probe, before any container
boots), four hard checks each fail with an actionable message naming the fix:

1. The host tailscaled socket is present
2. The host node is tagged with the credential's tag (fix hint: `sudo tailscale up --advertise-tags=<tag>`)
3. The host node appears in the credential's tailnet via the devices-list API - this is the check that catches a host daemon logged into a different tailnet than the OAuth client (fix hint: `sudo tailscale login` with the right account)
4. The host daemon is v1.86 or newer (Tailscale Services requirement)

The cloud check (3) is skipped on the headscale control seam alongside the
OAuth probe, since it cannot run there. Sidecar mode gains no new checks.

Per AGENTS.md, the devices-list call is SaaS-only: cover it with httptest unit
tests and keep the request byte-compatible with the real API call. Node tags
and version come from the existing local-daemon seam, which has a canned-JSON
fake.

**Blocked by:** 01 - `--mode` flag with sidecar default and sticky re-enable.

**Status:** in-review

- [x] `--mode host` with no host socket fails immediately, before the credential probe boots anything
- [x] `--mode host` with an untagged host node fails with the advertise-tags fix hint
- [x] `--mode host` with a host node absent from the credential's tailnet fails naming the tailnet mismatch
- [x] `--mode host` with a daemon older than 1.86 fails naming the version requirement
- [x] All four failures occur before any container is created or removed
- [x] Check 3 is skipped on the headscale control seam; checks 1, 2, 4 still apply there
- [x] A host node passing all four checks proceeds to a normal host-mode enable
