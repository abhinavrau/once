# 18 - Headscale harness: sidecar joins the control plane

**What to build:** Repoint the hidden env-var control seam at the Once-managed sidecar: a pre-minted auth key and control URL configure the sidecar's tailscaled instead of OAuth, exactly as research issues 03/09 scoped. Add one minimal headscale integration test proving the sidecar joins the control plane - keeping real-control-plane coverage of the container wiring (socket mount, state volume, userspace mode). The seam covers only "sidecar joins"; Services flows cannot run against headscale and are not attempted.

**Blocked by:** 12 (core swap).

**Status:** done

- [x] The control-seam env vars configure the sidecar's tailscaled (auth key + control URL) and skip the SaaS credential probe
- [x] One headscale integration test proves the sidecar registers with the control plane
- [x] The test runs in CI with no real tailnet or Tailscale credentials
- [x] Lint and integration tests pass

## Comments

2026-07-14: Verified done - no new code needed; the seam landed with the core swap (issue 12) and its verify/status siblings (13/14). ONCE_TAILSCALE_CONTROL_URL + ONCE_TAILSCALE_AUTH_KEY drive bootSidecar (TS_AUTHKEY plus TS_EXTRA_ARGS=--login-server=<url>, no --advertise-tags) in internal/docker/tailscale.go, and controlSeamActive() skips every SaaS-only flow: the enable-time OAuth probe (tailscale_validate.go), post-enable service verification (tailscale_verify.go), cloud-side status (tailscale_status.go), and the policy-provisioning offer (command/tailscale.go). TestTailscaleEnableBootsDocktailTopology in integration/tailscale_test.go boots headscale in the test's Docker network (pinned headscale/headscale:0.26.1, embedded DERP, no internet or credentials), mints a preauthkey, and asserts the sidecar registers and reports online under its container name - alongside the container-wiring checks the issue asked to keep (socket volume mount, state volume, TS_USERSPACE, no NET_ADMIN/TUN/host networking). Services flows are correctly not attempted against headscale (verify is skipped on the seam). Confirmed today: golangci-lint 0 issues, the headscale test passes in ~9s, and the full integration suite passes.
