Status: done

# `once tailscale enable`/`disable` + `once-tsdproxy` container lifecycle

## Parent

- PRD: `.scratch/tailscale-integration/PRD.md`
- ADR: `docs/adr/0001-tailscale-integration.md`

## What to build

The spine of the feature: a new nested `once tailscale` command family whose `enable`/`disable` subcommands boot and tear down the single `once-tsdproxy` helper container.

- `once tailscale enable --client-id <id> --client-secret <secret>` boots an `once-tsdproxy` container running `almeidapaulopt/tsdproxy:2` **pinned to an exact version** (same doctrine as the `basecamp/kamal-proxy:once-01` pin), on the `once` bridge network, in userspace networking mode (no `/dev/net/tun`, no net-admin). It mounts `/var/run/docker.sock` and an `once-tsdproxy-data` volume at `/data`, and is configured with `TSDPROXY_TAILSCALE_DEFAULT_CLIENTID`/`CLIENTSECRET` and `TSDPROXY_DOCKER_HOST=unix:///var/run/docker.sock`.
- Global Tailscale settings (OAuth Client ID + Secret) are serialized as JSON into the `once` label on the `once-tsdproxy` container itself, following the existing "settings live on the container they configure" doctrine. "Tailscale enabled" is implicit in the container's existence. Changing credentials recreates only `once-tsdproxy`.
- `once tailscale disable` stops and removes the `once-tsdproxy` container but **keeps** the `once-tsdproxy-data` volume, so node identities/Magic DNS names survive a re-enable without `writebook-1`-style suffixes. (Stripping app labels is slice 03.)
- **Hidden control-server seam**: boot honors undocumented env vars `ONCE_TAILSCALE_CONTROL_URL` / `ONCE_TAILSCALE_AUTH_KEY`, configuring TSDProxy with `controlUrl`+`authKey` instead of OAuth. No CLI/TUI surface — it exists to point `once-tsdproxy` at the headscale harness in tests, and is promotable to first-class headscale support later.

This slice does not yet expose any app or the admin panel; it only stands up and configures the proxy node.

## Acceptance criteria

- [ ] `once tailscale enable` boots `once-tsdproxy` from a pinned image with the Docker socket + `once-tsdproxy-data` volume mounted, in userspace mode
- [ ] OAuth Client ID/Secret are persisted as JSON in the `once` label on the `once-tsdproxy` container and passed through as the documented env vars
- [ ] `once tailscale disable` removes the container but retains the `once-tsdproxy-data` volume
- [ ] Re-running `enable` with new credentials recreates only `once-tsdproxy`
- [ ] `ONCE_TAILSCALE_CONTROL_URL`/`ONCE_TAILSCALE_AUTH_KEY` override the control plane when set; no CLI/TUI exposes them
- [ ] Integration test (using slice 01's harness) asserts the `once-tsdproxy` node registers against headscale via the control seam, and disable tears it down while keeping the volume

## Blocked by

- `.scratch/tailscale-integration/issues/01-headscale-harness-control-seam.md`
