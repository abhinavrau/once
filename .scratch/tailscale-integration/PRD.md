Status: ready-for-agent

# Product Requirement Document (PRD): Tailscale Integration & Private Discovery for Once

## Problem Statement

Users of Once self-host their Docker-based web applications on virtual private servers (VPS), home servers, or personal hardware. By default, exposing these applications requires mapping TCP ports (like 80 and 443) to the public internet, configuring custom DNS records, and setting up Let's Encrypt certificates. This presents two significant problems:
1. **Security Vulnerabilities**: Publicly exposed ports invite brute-force attacks, scanner probes, and exploitation of application vulnerabilities.
2. **Access Constraints**: Users want to access their self-hosted applications securely from any device (phone, laptop, work computer) without opening their infrastructure to the wider internet, but they also sometimes need to grant temporary public access to devices that cannot install the Tailscale client (e.g. work laptops, guest devices).

---

## Solution

Integrate Tailscale into Once using a single containerized reverse-proxy deployment strategy via **Tailscale Docker Proxy (TSDProxy)**. 
- All applications deployed via Once will be accessible on the user's private virtual network (tailnet) under unique Magic DNS hostnames (e.g., `https://writebook.tailnet-name.ts.net`).
- The admin interface (Once Admin Web App) is served on the host over a Unix domain socket and proxied onto the tailnet via Nginx, opening **zero host TCP ports** to the public or host network interfaces.
- Users can request **temporary public access** (via Tailscale Funnel) to individual application endpoints for a configurable duration (up to 24 hours, default 10 minutes). The Once background daemon will automatically tear down public access once the duration expires.

---

## User Stories

1. As a Once administrator, I want to configure my Tailscale OAuth credentials globally, so that Once can automate the provisioning and de-provisioning of Tailscale nodes for all my applications.
2. As a Once administrator, I want to access my Once admin panel securely via `https://once-admin.<tailnet>.ts.net` from any device on my tailnet, so that I don't expose administrative controls to the public internet.
3. As a Once administrator, I want to deploy a new application (e.g., Writebook) and have it automatically register on my tailnet with its own Magic DNS hostname, so that I can access it privately and securely.
4. As a Once administrator, I want Once to persist Tailscale node state across container restarts, so that my applications retain their identity and Magic DNS hostnames without incrementing (e.g. `writebook-1`, `writebook-2`).
5. As a Once administrator, I want Tailscale nodes to be de-registered automatically when I delete an application, so that my Tailscale admin portal remains clean and free of stale/inactive devices.
6. As a Once administrator, I want to enable temporary public access (Funnel) to a specific application, so that I can access it from a device (like a locked-down work laptop or a guest device) where I cannot install the Tailscale client.
7. As a Once administrator, I want to configure the duration of temporary Funnel access (up to 24 hours) when enabling it, so that I have fine-grained control over how long my app is exposed.
8. As a Once administrator, I want the Funnel access to default to 10 minutes, so that I don't accidentally leave an application exposed to the public internet for too long if I forget to configure the duration.
9. As a Once administrator, I want the background daemon to automatically disable Funnel access and recreate the application container without Funnel labels once the configured duration expires, so that the application is secured again automatically.
10. As a Once administrator, I want to view the remaining time of active temporary Funnel access on my admin dashboard, so that I know how much longer the public URL will remain valid.
11. As a Once administrator, I want to manually disable an active Funnel early before its duration expires, so that I can revoke public access immediately when I am done.
12. As a Once administrator, I want to view the FQDN and status of all my Tailscale nodes directly in the Once TUI dashboard and CLI, so that I can easily discover and connect to my services.
13. As a Once developer, I want to use standard public images (like `nginx:alpine` and `almeidapaulopt/tsdproxy:2`) instead of building custom Docker images, so that Once's packaging, build pipeline, and update delivery remain simple and lightweight.

---

## Implementation Decisions

### 1. Coexistence and Additive Routing
- **No Disruption**: The existing public/localhost routing via `kamal-proxy` (bound to host ports 80 and 443) remains the active default.
- **Parallel Proxies**: When Tailscale is enabled, TSDProxy (`once-tsdproxy`) runs alongside `kamal-proxy` in the private Docker bridge network. 
- **Exposed Endpoints**: Containers will carry labels for both proxies (e.g., `once` labels for `kamal-proxy` routing and `tsdproxy` labels for tailnet routing). Thus, an app remains accessible publicly at its domain, locally at its localhost address, and privately at its tailnet FQDN simultaneously.
- **All apps, retrofit on enable**: Tailnet exposure is all-or-nothing in v1. `once tailscale enable` performs a zero-downtime rolling recreation of every running app container to inject `tsdproxy.*` labels (Docker labels are immutable on running containers); all subsequent deploys get the labels automatically. `once tailscale disable` performs the same roll to remove them. Per-app opt-out is out of scope for v1.

### 2. TUI (Terminal User Interface) Integration
- **Global Settings Form**: Adding a global key binding `t` (or `Shift+T`) on the dashboard that triggers the **Global Tailscale Settings Form** overlay (fields: Enable Tailscale, OAuth Client ID, and OAuth Client Secret).
- **App Details viewport**: The expanded details card (`d` shortcut) will dynamically show:
  - `Public/Local URL`: Current public/localhost URL (e.g. `http://books.localhost`).
  - `Tailnet URL`: The private Magic DNS URL (e.g. `https://writebook.tailnet-name.ts.net`), if Tailscale is enabled.
  - `Funnel Status`: Shows status (`Inactive` or `Active (Expires in Xm)`) of the temporary Funnel.
- **App Settings Menu**: Add `7. Tailscale Funnel` option in the settings popup (`s` shortcut). This opens a sub-form to toggle Funnel and specify duration (default `10m`, max `24h`).

### 3. CLI (Command Line Interface) Integration
- Introduce a new nested command family `once tailscale`:
  - `once tailscale enable --client-id <id> --client-secret <secret>`: Sets up globally and boots `once-tsdproxy` and `once-admin`.
  - `once tailscale disable`: Rolls all app containers to strip `tsdproxy.*` labels (closing any active Funnels and clearing `FunnelExpiresAt`), then stops and deletes the `once-tsdproxy` and `once-admin` containers. The `once-tsdproxy-data` volume is **kept**, so node identities and Magic DNS hostnames survive a later re-enable without incrementing (e.g. no `writebook-1`).
  - `once tailscale status`: Lists node FQDNs, status, and active Funnel expirations.
- Introduce funnel commands nested under `tailscale` (mirroring the upstream `tailscale funnel` CLI, since Funnel is a Tailscale feature):
  - `once tailscale funnel enable <app-name> [--duration 10m]`: Enables temporary Funnel access.
  - `once tailscale funnel disable <app-name>`: Revokes public access immediately.
- Update existing commands:
  - `once list`: Appends the tailnet URL to each app's output line when Tailscale is enabled. (Note: `once list` currently prints simple `{Host} (status)` lines, not a table — the PRD's earlier "column" framing was inaccurate.)
  - `once teardown`: Also removes the `once-tsdproxy` and `once-admin` containers and deletes the `once-tsdproxy-data` volume (teardown is full cleanup, unlike `tailscale disable`).

### 4. Unified TSDProxy Container
- Once will deploy exactly one helper container named `once-tsdproxy` running the official TSDProxy image, **pinned to an exact version** (e.g. `almeidapaulopt/tsdproxy:2.x.y`) the same way `basecamp/kamal-proxy:once-01` is pinned today; the `once-admin` nginx image is likewise pinned (e.g. `nginx:1.xx-alpine`). Pins are bumped via Once releases through the existing self-update mechanism.
- **Settings storage**: The global Tailscale settings (OAuth Client ID, OAuth Client Secret) are serialized as JSON into the `once` label on the `once-tsdproxy` container itself, following the existing doctrine that settings live on the container they configure. "Tailscale enabled" is implicit in the container's existence (mirroring how the proxy works). Changing credentials recreates only `once-tsdproxy`; app traffic is unaffected. The secret is visible via `docker inspect` (label and env var) — the same exposure class as existing SMTP passwords in app labels.
- It will mount the Docker socket (`/var/run/docker.sock`) to monitor container labels and a state volume (`once-tsdproxy-data`) mounted to `/data` to persist Tailscale node keys.
- It will run in Userspace Networking Mode, requiring no net-admin privileges or `/dev/net/tun` devices on the host.
- **Hidden control-server seam**: The tsdproxy boot code supports overriding the control plane via undocumented env vars (`ONCE_TAILSCALE_CONTROL_URL`, `ONCE_TAILSCALE_AUTH_KEY`), which configure TSDProxy with `controlUrl` + `authKey` instead of OAuth (OAuth is Tailscale-SaaS-only). No CLI/TUI surface in v1 — this exists for integration testing against headscale, and can be promoted to first-class headscale support in a later release.
- **Ephemeral nodes**: All virtual Tailscale nodes are registered as ephemeral (`tsdproxy.ephemeral=true`). Deleting an app takes its node offline and Tailscale auto-removes it — the admin console stays clean (user story 5) and the hostname is freed, preventing `writebook-1`-style suffix collisions. Quick container restarts retain node identity via the persisted `/data` state (user story 4). Documented caveat: extended server downtime also removes nodes; they re-register under the same freed name when the server returns.

### 5. Unix-Socket Admin Proxy
- **Scope note**: The Admin Web App itself (its pages, endpoints, and capabilities) does not exist yet and is specified in a **separate PRD**. This PRD covers only the plumbing: the daemon's Unix-socket HTTP listener (serving a placeholder/health endpoint until the web app PRD lands), the `once-admin` nginx container, and its tailnet exposure.
- The Once background daemon will run the Admin Web App HTTP server on the host, listening exclusively on the Unix domain socket `/var/run/once-admin.sock`.
- Once will boot a helper container `once-admin` running `nginx:alpine` on the private `once` bridge network. It mounts `/var/run/once-admin.sock` and a daemon-generated nginx config file (written next to the socket, e.g. `/var/run/once-admin-nginx.conf`, bind-mounted read-only into `/etc/nginx/conf.d/`) that routes HTTP port 80 requests directly to the Unix socket.
- TSDProxy will reverse-proxy `once-admin.<tailnet>.ts.net` to the `once-admin` container. No TCP ports are opened on the host.

### 6. Dynamic Funnel Toggling and Expiration
- TSDProxy watches Docker container labels. Exposing an app is done by adding the `tsdproxy.enable=true` label, and Funnel is enabled by adding `tailscale_funnel` to the port label option (e.g. `tsdproxy.port.1=443/https:80/http, tailscale_funnel`).
- Once will add `FunnelExpiresAt *time.Time` to the `ApplicationSettings` struct, persisted in the app container's `once` label — written atomically in the same container recreation that adds/removes the `tailscale_funnel` label option, so the expiry and the funnel state can never drift apart. (The state JSON keeps only operation results; optionally a `LastFunnelTeardown OperationResult` is recorded there, matching the `LastBackup` pattern.)
- To enable Funnel, Once calculates the expiration timestamp, sets it in the application settings, applies the updated labels, and recreates the container.
- **Daemon required**: Enabling a Funnel (CLI or TUI) fails with a clear error if the background service is not installed and running — without the daemon, automatic teardown could never happen and the app would stay public indefinitely.
- **Exact expiry**: The background daemon's loop wakes at the earlier of its regular 5-minute tick or the soonest `FunnelExpiresAt`, so a 10-minute Funnel closes at 10 minutes (not up to 15). Because expiry timestamps are persisted in state, teardown survives daemon restarts.
- On expiry, the daemon clears the field in the state, removes the `tailscale_funnel` option from the container labels, and recreates the container to close the Funnel.

### 7. Local URL Lookup API
- Once (daemon, CLI, and TUI — all host processes) discovers registered FQDNs and node status by querying TSDProxy's lookup API (`/api/v1/proxies`). Since the host cannot resolve container DNS names (and macOS Docker Desktop cannot reach bridge IPs), the `once-tsdproxy` container publishes its API port bound to loopback only: `127.0.0.1:8484 → 8080`.
- This loopback binding is the single deliberate exception to the "zero host TCP ports" constraint: it is unreachable from any network interface, so nothing is exposed beyond the host itself.
- This local lookup is offline-friendly and avoids Tailscale SaaS API rate-limits.
- **API authentication**: TSDProxy's API requires auth (Tailscale identity, Bearer API key, or localhost). Connections arriving via a Docker-published port appear to come from the bridge gateway IP, not localhost, so Once generates an API key at enable time, configures TSDProxy with it, and sends it as a Bearer token. The key is stored with the Tailscale settings in the `once-tsdproxy` container label.

---

## Testing Decisions

### 1. Test Seams
- **Daemon Socket Listener**: Test the Unix domain socket HTTP server by writing unit tests that spin up the socket listener locally and use Go's `net.Dialer` with `unix` network protocol to perform API requests.
- **State Logic**: Test the `State` struct's expiration logic by writing unit tests that set `FunnelExpiresAt` in the past/future and verify the daemon's expiration checker correctly identifies whether Funnel teardown is due.
- **Docker Label Generation**: Test that Once correctly translates application settings (with or without Funnel and Tailscale enabled) into the appropriate `tsdproxy.*` labels during container configuration.

### 2. Integration Tests Against a Local Headscale Control Plane
- **No Tailscale SaaS dependency in CI**: Integration tests boot a local [headscale](https://github.com/juanfont/headscale) container (the open-source Tailscale control server) inside the test's Docker network. TSDProxy is verified to support this via `controlUrl` + `authKey`; Once's hidden control-server seam (see Implementation Decisions §4) points `once-tsdproxy` at it. A pre-auth key is created via `docker exec <headscale> headscale preauthkeys create --user once-test --reusable`.
- **Test app image**: `traefik/whoami` (pinned tag) — a ~7 MB static binary listening on port 80 (matching Once's port-80 assumption) that echoes the serving container's hostname and request headers, so tests can assert which backend served a request through the proxy chain. Much faster than the `once-campfire` image used by the existing integration tests.
- **Assertion depth (hybrid)**: Most tests assert at the control/API layer — TSDProxy's `/api/v1/proxies` reports the proxy `Running` with the expected FQDN, and `headscale nodes list` shows the node registered. One end-to-end smoke test additionally boots a `tailscale` client container joined to the same headscale instance and curls the whoami app through the tailnet via its Magic DNS name, asserting the echoed hostname — proving the userspace-networking data path once without paying node-join latency in every scenario.
- **HTTPS caveat**: `ts.net` Let's Encrypt certificates are a SaaS feature; headscale-based tests use plain-HTTP port labels (e.g. `80/http:80/http`) and make no certificate assertions.
- **Funnel testing**: Headscale cannot exercise Funnel (it depends on Tailscale's SaaS ingress). **The headscale-based integration suite must contain no Funnel scenarios and never set the `tailscale_funnel` port option** — TSDProxy attempting Funnel activation against headscale would put the proxy into an error state and fail tests misleadingly. CI covers everything Once owns via unit tests (funnel label generation, expiry math, daemon teardown-on-expiry, container recreation). Live Funnel verification happens in the SaaS smoke pass below.
- **Shared harness, two control-plane backends**: The integration harness is abstracted over a control-plane backend — `headscale` (default) or `tailscale-saas`. The full suite runs against headscale in CI. When `ONCE_TEST_TS_OAUTH_CLIENT_ID`/`ONCE_TEST_TS_OAUTH_CLIENT_SECRET` are present, a small **SaaS smoke pass** runs the scenarios only the real control plane can prove (skipped otherwise; run manually before releases):
  - OAuth client-credential authentication path (headscale only exercises the authkey seam).
  - Real `ts.net` FQDN registration and HTTPS certificate provisioning (with a generous timeout — first-time cert issuance can take a minute+).
  - Funnel public reachability: curl the funneled app's public URL from the test host — no tailnet client container needed, and this single check proves the entire data path end-to-end.
  - Ephemeral node auto-cleanup after app deletion (SaaS-side removal behavior headscale may not replicate exactly).
  - SaaS test nodes use unique per-run name suffixes to avoid Magic DNS collisions between concurrent or aborted runs; ephemeral registration keeps the maintainer tailnet self-cleaning.

### 3. Prior Art
- Check `internal/docker/application_settings_test.go` and `internal/docker/state_test.go` for examples of testing settings serialization, comparison, and state changes.
- Check `internal/background/runner_test.go` for prior art on mocking state files and verifying background tasks (like backups and updates).
- Check `integration/docker_test.go` for the existing namespace/proxy/deploy harness the headscale tests should extend.

---

## Out of Scope
- Support for multiple tailnets or multiple OAuth credentials within a single Once namespace.
- Direct management of Tailscale user ACLs or security policies from within the Once UI (users must configure Funnel permissions in their Tailscale admin console).
- Funnel durations longer than 24 hours.

---

## Further Notes
- The default 10-minute duration is designed to be safe for quick administrative checks or file transfers on non-tailnet devices.
- The `80/http` upstream in the `tsdproxy.port.1` label matches Once's existing system-wide assumption that app containers listen on port 80 (kamal-proxy deploys with no explicit target port, which defaults to 80).
- **Funnel prerequisite**: Funnel only works if the tailnet's ACL policy grants the `funnel` node attribute. Once cannot manage ACLs (see Out of Scope); when TSDProxy fails to activate a Funnel for this reason, Once must surface the error to the user rather than reporting the Funnel as active.
- TSDProxy label syntax (`tsdproxy.enable`, `tsdproxy.name`, `tsdproxy.port.N`, `tailscale_funnel` port option, `tsdproxy.ephemeral`) and the `GET /api/v1/proxies` endpoint were verified against the official TSDProxy v2 documentation.
- Because container recreation is required to update Docker labels, toggling the Funnel will trigger a zero-downtime rolling restart of the application container, managed by `kamal-proxy`.

