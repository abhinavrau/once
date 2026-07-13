# 18 - Headscale harness: sidecar joins the control plane

**What to build:** Repoint the hidden env-var control seam at the Once-managed sidecar: a pre-minted auth key and control URL configure the sidecar's tailscaled instead of OAuth, exactly as research issues 03/09 scoped. Add one minimal headscale integration test proving the sidecar joins the control plane - keeping real-control-plane coverage of the container wiring (socket mount, state volume, userspace mode). The seam covers only "sidecar joins"; Services flows cannot run against headscale and are not attempted.

**Blocked by:** 12 (core swap).

**Status:** ready-for-agent

- [ ] The control-seam env vars configure the sidecar's tailscaled (auth key + control URL) and skip the SaaS credential probe
- [ ] One headscale integration test proves the sidecar registers with the control plane
- [ ] The test runs in CI with no real tailnet or Tailscale credentials
- [ ] Lint and integration tests pass
