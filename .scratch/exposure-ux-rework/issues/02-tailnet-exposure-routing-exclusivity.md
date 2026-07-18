# 02 - Tailnet exposure and routing exclusivity

**What to build:** `--expose tailnet` puts an app on the tailnet, and nowhere else. The app is advertised as a Tailscale Service named from the App Name (overridable via the same `--host` knob) and registers no Proxy route; conversely local and public apps advertise no Service. No configuration can produce two exposures at once. Switching exposure via `update --expose` moves the app cleanly between routing paths. Deploying `--expose tailnet` before Tailscale is enabled fails with a clear pointer to `once tailscale enable`. Funnel's prerequisite becomes exposure `tailnet` instead of the old advertised boolean. The hostname-shadowing warning remains the collision guard for service names.

**Blocked by:** 01 - Exposure concept with local/public semantics.

**Status:** ready-for-agent

- [ ] `--expose tailnet` advertises a Service named after the App Name and registers no Proxy route
- [ ] `--host` overrides the service name under tailnet exposure
- [ ] Local and public apps advertise no Service; no state can hold two exposures
- [ ] `update --expose` transitions an app between all three exposures, tearing down the old routing path
- [ ] Tailnet deploy before enable fails with the pointer to `once tailscale enable`
- [ ] Funnel enable is refused unless the app's exposure is tailnet
- [ ] Headscale-seam integration tests are rewritten from `--tailscale` to `--expose tailnet` and pass
