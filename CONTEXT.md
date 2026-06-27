# Once
Once is a CLI/TUI platform for installing and self-hosting web applications on Docker.

## Core

**Namespace**:
A name prefix (default `once`) that scopes one Once installation — its proxy, apps, volumes, and supporting containers — so multiple installations can coexist on one host.

**Application**:
A self-hosted web app that Once manages, defined by its image, host, and settings, and run as one or more app containers.
_Avoid_: service, deployment.

**App Container**:
The Docker container running an Application, named `{namespace}-app-{appName}-{randomID}`. More than one may run briefly for the same Application during a zero-downtime roll.

**Random ID**:
The random 6-character hex suffix on an app container's name that lets a replacement container boot alongside the old one without a naming collision. It is generated before the container exists, so it is unrelated to Docker's container ID.
_Avoid_: short ID; container ID — the suffix is not a truncation of Docker's container ID (that truncation is a distinct value, `shortContainerID`, used as the proxy routing target).

**Proxy**:
The single kamal-proxy container (`{namespace}-proxy`) that terminates TLS and routes inbound traffic to app containers by host.
_Avoid_: load balancer, gateway, router.

**Host**:
The public DNS hostname for an Application; the proxy routes a request to an app container by matching it.
_Avoid_: domain, URL.

## State

**Configuration**:
The settings describing what Once has set up — apps, hosts, TLS, ports, encryption keys — stored as JSON in the `once` label on containers and volumes, never in a separate file. Distinct from Application Data. In code each component's configuration is a `…Settings` struct.
_Avoid_: config file.

**Application Data**:
An app's own persistent contents (databases, uploads, and so on), living in its Application Data Volume. Once provides the volume but has no opinion on what's inside.
_Avoid_: storage, state.

**Application Data Volume**:
The Docker volume (`{namespace}-app-{appName}`) mounted into the app container at `/storage` and `/rails/storage`, persisting Application Data across container replacements. Its `once` label also holds the app's generated encryption keys (secret key base, VAPID pair).
_Avoid_: storage volume, data dir.

**`once` label**:
The Docker label, keyed `once`, that holds a component's Configuration as JSON — app settings on the app container, proxy settings on the proxy, encryption keys on the volume.

## Lifecycle operations

**Deploy**:
Install and start an Application for the first time — pull the image, create the volume, create the app container, and register its host with the proxy.

**Roll**:
Replace an Application's running container with a fresh one at zero downtime, reusing the same image and volume and picking up Configuration changes.
_Avoid_: restart, recreate.

**Update**:
Pull an Application's image and roll it only if the image changed.
_Avoid_: upgrade.

**Boot**:
Bring up a supporting container (Proxy, Admin, TSDProxy) idempotently. Distinct from Deploy, which is for Applications.
_Avoid_: start.

**Start / Stop**:
Resume or pause an existing app container without recreating it. Distinct from Boot and Deploy.

**Backup / Restore**:
Capture an Application's Configuration and Application Data to a `.tar.gz` file, and recreate the Application from one.

## Tailscale

**Tailnet**:
A private virtual network built using Tailscale.

**Magic DNS**:
Tailscale's automatic name registration and resolution service that assigns a unique, secure domain name to each node in a tailnet.

**Tailscale Docker Proxy (TSDProxy)**:
A single utility container running Tailscale's embedded `tsnet` library that watches Docker events and dynamically exposes containers to the tailnet as virtual Tailscale nodes.
_Avoid_: Tailscale Sidecar, Tailscale Agent container.

**Tailscale Funnel**:
A Tailscale feature that routes public internet traffic to a specific port on a tailnet node, enabling temporary public access.

## Interfaces

**Once Admin Web App**:
A browser-based administrative interface for Once, reachable only from the tailnet. Distinct from the TUI and CLI. Its feature set is specified separately from the Tailscale integration that exposes it.
