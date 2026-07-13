# 11 - Docktail-compatible credential probe

**What to build:** Update the enable-time OAuth credential validation so it probes exactly what Docktail needs. The probe must be byte-compatible with Docktail's real Tailscale API usage (successor to the current mint-and-delete probe; scopes per research issue 05), and must fail fast with a clear, distinct message when: credentials are wrong, a required scope is missing, or the configured tag is not owned by the client. Tag normalization behavior carries over unchanged.

From the user's perspective: `once tailscale enable` with bad credentials tells me the real problem (which scope, which tag) instead of leaving me to debug containers (user story 12).

**Blocked by:** None - can start immediately.

**Status:** done

- [x] Probe requests match Docktail's real API shapes byte-for-byte, asserted in unit tests (Seam 1: httptest baseURL parameterization, following the existing credential-probe suite as prior art)
- [x] Wrong credentials, missing scope, and unowned tag each produce a distinct actionable error before any container is booted
- [x] Tag normalization tests carry over and pass
- [x] The headscale control seam still skips the SaaS probe
- [x] Lint and unit tests pass

## Comments

2026-07-13: Implemented in internal/docker/tailscale_validate.go. The probe now runs: token exchange -> mint-and-delete of a sidecar-shaped auth key (auth_keys scope, tag ownership) -> GET/PUT/GET-devices/DELETE of a throwaway `svc:once-probe` Service definition (services + devices:core scopes, tag defined in tagOwners), each with a distinct actionable error. Service requests are byte-compatible with Docktail's putService/getService/listServiceHosts/deleteService, asserted exactly in unit tests; the "-" tailnet segment matches Docktail's TAILSCALE_TAILNET default. The probe Service carries a marker comment so a leftover from an interrupted probe is reused, while a foreign Service with that name is never overwritten or deleted. Note for issue 12: the sidecar boot must mint its TS_AUTHKEY with the same shape the probe mints (description "once-tailscale", single-use, non-ephemeral, preauthorized, tagged) to keep the lockstep guarantee.
