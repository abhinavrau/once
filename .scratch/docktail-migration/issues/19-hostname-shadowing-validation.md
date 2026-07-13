# 19 - Hostname-shadowing validation

**What to build:** A service name that is not a valid DNS label, or that would collide with an existing tailnet machine hostname (a machine's Magic DNS entry shadows a service at the same FQDN), is flagged to the user at enable time and at app deploy time while Tailscale is enabled - so an app URL never silently resolves to the wrong node (user story 22). Shadowing produces a warning naming the conflicting machine; an invalid DNS label is rejected.

**Blocked by:** 12 (core swap).

**Status:** ready-for-agent

- [ ] Invalid-DNS-label app names are rejected at enable/deploy with a clear message
- [ ] A service name matching a tailnet machine hostname produces a warning naming the machine
- [ ] Checks run at both enable (all exposed apps) and deploy (the new app) when Tailscale is enabled
- [ ] Covered by unit tests through the daemon fake / httptest API
- [ ] Lint and unit tests pass
