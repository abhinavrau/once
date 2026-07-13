# 11 - Docktail-compatible credential probe

**What to build:** Update the enable-time OAuth credential validation so it probes exactly what Docktail needs. The probe must be byte-compatible with Docktail's real Tailscale API usage (successor to the current mint-and-delete probe; scopes per research issue 05), and must fail fast with a clear, distinct message when: credentials are wrong, a required scope is missing, or the configured tag is not owned by the client. Tag normalization behavior carries over unchanged.

From the user's perspective: `once tailscale enable` with bad credentials tells me the real problem (which scope, which tag) instead of leaving me to debug containers (user story 12).

**Blocked by:** None - can start immediately.

**Status:** ready-for-agent

- [ ] Probe requests match Docktail's real API shapes byte-for-byte, asserted in unit tests (Seam 1: httptest baseURL parameterization, following the existing credential-probe suite as prior art)
- [ ] Wrong credentials, missing scope, and unowned tag each produce a distinct actionable error before any container is booted
- [ ] Tag normalization tests carry over and pass
- [ ] The headscale control seam still skips the SaaS probe
- [ ] Lint and unit tests pass
