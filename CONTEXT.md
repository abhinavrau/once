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

**App Name**:
An Application's base identity, derived from its image reference unless overridden. Addresses under every Exposure derive from it.

**Host**:
The name an Application answers to, interpreted by its Exposure: `{app name}.localhost` under Local, the Tailscale Service name under Tailnet, a real DNS FQDN under Public. Derived from the App Name when not given - except under Public, where it is required.
_Avoid_: domain, URL, public hostname.

## Exposure

**Exposure**:
The single, explicitly chosen reachability level of an Application. Exactly one per Application at a time; never derived from other settings.
_Avoid_: visibility, access mode.

**Local (exposure)**:
Reachable only from the machine Once runs on.

**Tailnet (exposure)**:
Reachable only from the tailnet, as a Tailscale Service.

**Public (exposure)**:
Reachable from the public internet via the Proxy with TLS.

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
A binary never destroys label fields it does not model: unknown fields are carried through every rewrite, and only fields explicitly deleted from the schema (the removed-field denylist) are shed - the preservation doctrine, established by commit 3e47d59 and generalized by ADR-0008.

**Label Codec**:
The one module (`internal/docker/label.go`) that round-trips a `…Settings` struct through the `once` label: key, JSON encode/decode, unknown-field preservation, removed-field denylist. Every settings type goes through it; none hand-writes its own marshalling (ADR-0008).

## Lifecycle operations

**Deploy**:
Install and start an Application for the first time — pull the image, create the volume, create the app container, and register its host with the proxy.

**Roll**:
Replace an Application's running container with a fresh one at zero downtime, reusing the same image and volume and picking up Configuration changes.
_Avoid_: restart, recreate.

**Apply Settings**:
Validate an Application's changed Configuration, check the new Host is free, roll the container to pick it up, and restore the previous Configuration if the roll fails. Distinct from Roll, which is only the container replacement.
_Avoid_: update - that is the image-driven operation.

**Update**:
Pull an Application's image and roll it only if the image changed. Image-driven only: changing Configuration is Apply Settings, even though the CLI spells it `once update`.
_Avoid_: upgrade.

**Boot**:
Bring up a supporting container (Proxy, Admin, Docktail, Tailscale sidecar) idempotently. Distinct from Deploy, which is for Applications.
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

**Tailscale Service**:
Tailscale's first-party primitive for exposing a service on the tailnet under a stable `svc:` name with its own Magic DNS entry, advertised by a host node rather than registered as a device of its own.

**Docktail**:
A single utility container (`once-docktail`) that watches Docker labels and advertises labelled containers as Tailscale Services through one real `tailscaled` - the unprivileged `once-tailscale` sidecar Once boots by default, or the host's own daemon when the user explicitly opts in.
_Avoid_: TSDProxy, Tailscale Docker Proxy.

**Runtime Credential**:
The single minimal-scope, tagged OAuth client the user creates directly in the Tailscale admin console and supplies when enabling Tailscale - the only Tailscale credential that ever exists. Stored and used for ongoing Tailscale operations. Its secret reaches Once only through the environment, never on the command line (ADR-0013); Once does not protect it at rest.
_Avoid_: bootstrap credential, policy credential - the minting hierarchy they named was removed by ADR-0006.

**Tailscale Funnel**:
A Tailscale feature that routes public internet traffic to a specific port on a tailnet node, enabling temporary public access.

**Mode Resolution**:
The enable-time rule (ADR-0007) turning a requested Tailscale mode into the effective one - explicit wins, empty keeps the stored mode, fresh enables default to the sidecar. Exists only at enable time; one implementation (ADR-0010).

**Daemon Binding**:
Mapping a resolved or stored mode to the Tailscale Daemon that serves it - host CLI for host mode, docker exec into the sidecar otherwise. One function; provider-shaped, so it must not be duplicated (ADR-0010).

**Session (Tailscale)**:
The per-operation resolution every Tailscale read path starts with - load the stored settings, bind the daemon they imply, hand both down. Distinct from Mode Resolution: sessions never see a requested mode.

**Control Seam**:
The hidden headscale env-var pair (pre-minted auth key + control URL) that replaces SaaS OAuth so integration tests run against headscale (ADR-0002). Read through one accessor only; invisible outside `internal/docker`. Its `ONCE_TAILSCALE_` prefix is shared with one public variable (ADR-0013), so the prefix alone does not mark a variable as seam-only.

**Daemon Seam**:
`TailscaleDaemon` - how Once reads state from a local tailscaled (host CLI or sidecar docker exec). One of the Tailscale subsystem's two seams to the outside world.

**SaaS Seam**:
`tailscaleAPI` - how Once reaches the Tailscale control-plane HTTP API (OAuth exchange, key mint, Service definitions, device lists). Deliberately Tailscale-specific: a second VPN provider gets a sibling seam, not a slot in a generic provider interface (ADR-0011).

## Interfaces

**Once Admin Web App**:
A browser-based administrative interface for Once, reachable only from the tailnet. Distinct from the TUI and CLI. Its feature set is specified separately from the Tailscale integration that exposes it.
