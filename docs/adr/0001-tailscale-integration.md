# Tailscale Integration Architecture

To enable applications deployed via Once to be securely accessible on a private virtual network, we decided to integrate Tailscale.

We chose to deploy a single **Tailscale Docker Proxy (TSDProxy)** container (`once-tsdproxy`) running Tailscale's embedded `tsnet` library. TSDProxy watches the Docker daemon via `/var/run/docker.sock` and automatically exposes containers labeled with `tsdproxy.enable=true` to the tailnet as virtual Tailscale nodes with unique Magic DNS names. This eliminates the need for a dedicated sidecar container per application.

To keep container execution portable and secure, the virtual Tailscale nodes run in **Userspace Networking Mode** natively via `tsnet`, avoiding host `/dev/net/tun` dependencies or container net admin privileges.

To authenticate virtual nodes, we use **Tailscale OAuth Client Credentials** configured globally in Once. Once configures TSDProxy with these credentials, enabling it to dynamically request short-lived, single-use auth keys from the Tailscale API. Local Tailscale state is persisted in a single shared volume (`once-tsdproxy-data`) managed by TSDProxy.

To manage configurations securely without opening host TCP ports, the **Once Admin Web App** is exposed via an Nginx proxy container (`once-admin`) on the private Docker bridge network, mounting a Unix domain socket to communicate with the host's background daemon. TSDProxy is labeled to expose this Nginx container to the tailnet, keeping the admin interface strictly private to the tailnet. Funnel access is toggled dynamically by updating the target container's labels, which TSDProxy automatically detects and reconciles.

To display the registered tailnet URLs (FQDNs) for the admin web app and application containers inside the Once CLI and TUI dashboard, Once queries TSDProxy's local REST API endpoint (`http://once-tsdproxy:8080/api/v1/proxies`) inside the Docker network. This keeps the URL discovery self-contained and independent of the global Tailscale cloud API.
