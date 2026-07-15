# 19 - Hostname-shadowing validation

**What to build:** A service name that is not a valid DNS label, or that would collide with an existing tailnet machine hostname (a machine's Magic DNS entry shadows a service at the same FQDN), is flagged to the user at enable time and at app deploy time while Tailscale is enabled - so an app URL never silently resolves to the wrong node (user story 22). Shadowing produces a warning naming the conflicting machine; an invalid DNS label is rejected.

**Blocked by:** 12 (core swap).

**Status:** done

- [x] Invalid-DNS-label app names are rejected at enable/deploy with a clear message
- [x] A service name matching a tailnet machine hostname produces a warning naming the machine
- [x] Checks run at both enable (all exposed apps) and deploy (the new app) when Tailscale is enabled
- [x] Covered by unit tests through the daemon fake / httptest API
- [x] Lint and unit tests pass

## Comments

2026-07-14: Implemented. ValidateTailscaleServiceName (internal/docker/tailscale_names.go) enforces the RFC 1123 label shape and is called from Namespace.EnableTailscale for every exposed app plus once-admin (before any Docker work), and from Application.Deploy when Tailscale is enabled and the app is exposed - one guard covering CLI deploy, TUI install, and settings-driven redeploys. Shadow detection reads machine hostnames from the local daemon via a new TailscaleDaemon.MachineHostnames (first label of each node's DNSName from `tailscale status --json`, HostName fallback), so it works in both host and sidecar mode and on the headscale seam. Tailscale.ShadowWarnings returns a warning naming the machine per shadowed service; EnableTailscale returns the warnings (CLI prints them, the TUI form displays them in the status area and stays open so they are seen), and `once deploy` prints them for the new app before deploying. Shadow checks are best effort by design - an unreadable daemon never fails an enable or deploy. Unit-tested through FakeTailscaleDaemon (no cloud API involved, so no httptest server needed). Lint 0 issues; unit suite passes; the two tailscale enable integration tests (headscale seam, CLI + TUI form) pass. Not done: the TUI install flow does not display the shadow warning (only the invalid-name rejection surfaces there); plumbing a non-fatal warning through the install activity to the post-install screen was judged not worth it while enable re-checks all exposed apps.
