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
  - `once tailscale disable`: Stops and deletes the Tailscale system containers.
  - `once tailscale status`: Lists node FQDNs, status, and active Funnel expirations.
- Introduce funnel commands:
  - `once funnel enable <app-name> [--duration 10m]`: Enables temporary Funnel access.
  - `once funnel disable <app-name>`: Revokes public access immediately.
- Update existing commands:
  - `once list`: Appends a `Tailnet URL` column to the output table when Tailscale is enabled.

### 4. Unified TSDProxy Container
- Once will deploy exactly one helper container named `once-tsdproxy` running the official `almeidapaulopt/tsdproxy:2` image.
- It will mount the Docker socket (`/var/run/docker.sock`) to monitor container labels and a state volume (`once-tsdproxy-data`) mounted to `/data` to persist Tailscale node keys.
- It will run in Userspace Networking Mode, requiring no net-admin privileges or `/dev/net/tun` devices on the host.

### 5. Unix-Socket Admin Proxy
- The Once background daemon will run the Admin Web App HTTP server on the host, listening exclusively on the Unix domain socket `/var/run/once-admin.sock`.
- Once will boot a helper container `once-admin` running `nginx:alpine` on the private `once` bridge network. It mounts `/var/run/once-admin.sock` and routes HTTP port 80 requests directly to the Unix socket.
- TSDProxy will reverse-proxy `once-admin.<tailnet>.ts.net` to the `once-admin` container. No TCP ports are opened on the host.

### 6. Dynamic Funnel Toggling and Expiration
- TSDProxy watches Docker container labels. Exposing an app is done by adding the `tsdproxy.enable=true` label, and Funnel is enabled by adding `tailscale_funnel` to the port label option (e.g. `tsdproxy.port.1=443/https:80/http, tailscale_funnel`).
- Once will update the `AppState` struct to store `FunnelExpiresAt *time.Time` and the current Funnel configuration.
- To enable Funnel, Once calculates the expiration timestamp, saves it to the state JSON, modifies the app container labels, and recreates the container.
- The background daemon check loop (running every 5 minutes) will check if the current time is past `FunnelExpiresAt`. If expired, it will clear the field in the state, remove the `tailscale_funnel` option from the container labels, and recreate the container to close the Funnel.

### 7. Local URL Lookup API
- Once daemon will discover registered FQDNs by querying `http://once-tsdproxy:8080/api/v1/proxies` from the host. This local lookup is offline-friendly and avoids Tailscale SaaS API rate-limits.

---

## Testing Decisions

### 1. Test Seams
- **Daemon Socket Listener**: Test the Unix domain socket HTTP server by writing unit tests that spin up the socket listener locally and use Go's `net.Dialer` with `unix` network protocol to perform API requests.
- **State Logic**: Test the `State` struct's expiration logic by writing unit tests that set `FunnelExpiresAt` in the past/future and verify the daemon's expiration checker correctly identifies whether Funnel teardown is due.
- **Docker Label Generation**: Test that Once correctly translates application settings (with or without Funnel and Tailscale enabled) into the appropriate `tsdproxy.*` labels during container configuration.

### 2. Prior Art
- Check `internal/docker/application_settings_test.go` and `internal/docker/state_test.go` for examples of testing settings serialization, comparison, and state changes.
- Check `internal/background/runner_test.go` for prior art on mocking state files and verifying background tasks (like backups and updates).

---

## Out of Scope
- Support for multiple tailnets or multiple OAuth credentials within a single Once namespace.
- Direct management of Tailscale user ACLs or security policies from within the Once UI (users must configure Funnel permissions in their Tailscale admin console).
- Funnel durations longer than 24 hours.

---

## Further Notes
- The default 10-minute duration is designed to be safe for quick administrative checks or file transfers on non-tailnet devices.
- Because container recreation is required to update Docker labels, toggling the Funnel will trigger a zero-downtime rolling restart of the application container, managed by `kamal-proxy`.

