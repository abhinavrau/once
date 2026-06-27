Status: done

# Unix-socket admin daemon listener + `once-admin` nginx container

## Parent

- PRD: `.scratch/tailscale-integration/PRD.md`
- ADR: `docs/adr/0001-tailscale-integration.md`

## What to build

Expose an admin endpoint on the tailnet with **zero host TCP ports**, via a Unix-socket-backed nginx proxy.

**Scope note**: the Admin Web App's pages/endpoints/capabilities are specified in a *separate PRD* and do not exist yet. This slice is plumbing only — the daemon socket listener (serving a placeholder/health endpoint until the web app lands), the `once-admin` container, and its tailnet exposure.

- The background daemon runs an HTTP server listening **exclusively** on the Unix domain socket `/var/run/once-admin.sock`, serving a placeholder/health endpoint.
- `once tailscale enable` boots an `once-admin` helper container running `nginx:alpine` **pinned to an exact version** (e.g. `nginx:1.xx-alpine`), on the private `once` bridge network with no host ports. It mounts `/var/run/once-admin.sock` and a daemon-generated nginx config (written next to the socket, e.g. `/var/run/once-admin-nginx.conf`, bind-mounted read-only into `/etc/nginx/conf.d/`) that routes HTTP port 80 to the Unix socket.
- The `once-admin` container carries `tsdproxy.enable=true` / `tsdproxy.name=once-admin` (reusing the label helper from slice 03) so TSDProxy reverse-proxies `once-admin.<tailnet>.ts.net` to it.

## Acceptance criteria

- [ ] The daemon serves an HTTP placeholder/health endpoint on `/var/run/once-admin.sock` and on no TCP port
- [ ] Unit test spins up the socket listener locally and performs requests using `net.Dialer` with the `unix` network
- [ ] `once tailscale enable` boots a pinned `nginx:alpine` `once-admin` container mounting the socket + generated config, with no published host ports
- [ ] The generated nginx config routes port-80 HTTP to the Unix socket
- [ ] `once-admin` carries the `tsdproxy.enable`/`tsdproxy.name=once-admin` labels
- [ ] Integration test (slice 01 harness): the admin health endpoint is reachable through the tailnet at the `once-admin` FQDN

## Blocked by

- `.scratch/tailscale-integration/issues/02-tailscale-enable-disable-tsdproxy-container.md`
- `.scratch/tailscale-integration/issues/03-app-tsdproxy-labels-retrofit-roll.md`
