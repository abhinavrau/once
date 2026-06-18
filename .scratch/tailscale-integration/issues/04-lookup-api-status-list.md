Status: done

# Local lookup API client + loopback API port/key + `once tailscale status` + `once list` tailnet URL

## Parent

- PRD: `.scratch/tailscale-integration/PRD.md`
- ADR: `docs/adr/0001-tailscale-integration.md`

## What to build

Let host processes (daemon, CLI, TUI) discover registered FQDNs and node status without depending on the Tailscale cloud API, by querying TSDProxy's local lookup API.

- Add a loopback-only published port to the `once-tsdproxy` container config: `127.0.0.1:8484 → 8080`. This is the single deliberate exception to the "zero host TCP ports" constraint — unreachable from any network interface, so nothing is exposed beyond the host. (Needed because the host cannot resolve container DNS names, and macOS Docker Desktop cannot reach bridge IPs.)
- **API auth**: TSDProxy's API requires auth, and connections via a Docker-published port appear to come from the bridge gateway IP (not localhost). So at enable time Once generates an API key, configures TSDProxy with it, and stores it alongside the Tailscale settings in the `once-tsdproxy` container label. The host client sends it as a Bearer token.
- Build a host-side client for `GET /api/v1/proxies` returning per-proxy FQDN + running status (and funnel expiry once that exists).
- `once tailscale status` lists node FQDNs, status, and any active Funnel expirations.
- `once list` appends each app's tailnet URL to its existing `{Host} (status)` output line when Tailscale is enabled (it prints simple lines, not a table).

## Acceptance criteria

- [ ] `once-tsdproxy` publishes its API on `127.0.0.1:8484` only; an API key is generated at enable time, configured into TSDProxy, and stored in the `once-tsdproxy` label
- [ ] A client queries `/api/v1/proxies` with the Bearer key and parses FQDN + status
- [ ] `once tailscale status` prints node FQDNs, status, and active Funnel expirations
- [ ] `once list` appends the tailnet URL per app when Tailscale is enabled, and is unchanged when it isn't
- [ ] Integration test (slice 01 harness) asserts the API reports the expected FQDN(s) as `Running`

## Blocked by

- `.scratch/tailscale-integration/issues/02-tailscale-enable-disable-tsdproxy-container.md`
- `.scratch/tailscale-integration/issues/03-app-tsdproxy-labels-retrofit-roll.md`
