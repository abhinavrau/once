# Once
Once is a CLI/TUI platform for installing and self-hosting web applications on Docker.

## Language

**Tailnet**:
A private virtual network built using Tailscale.

**Magic DNS**:
Tailscale's automatic name registration and resolution service that assigns a unique, secure domain name to each node in a tailnet.

**Tailscale Docker Proxy (TSDProxy)**:
A single utility container running Tailscale's embedded `tsnet` library that watches Docker events and dynamically exposes containers to the tailnet as virtual Tailscale nodes.
_Avoid_: Tailscale Sidecar, Tailscale Agent container

**Tailscale Funnel**:
A Tailscale feature that routes public internet traffic to a specific port on a tailnet node, enabling temporary public access.

**Once Admin Web App**:
A browser-based administrative interface for Once, reachable only from the tailnet. Distinct from the TUI and CLI. Its feature set is specified separately from the Tailscale integration that exposes it.
