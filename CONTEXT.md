# Once
Once is a CLI/TUI platform for installing and self-hosting web applications on Docker.

## Core

**Namespace**:
A name prefix (default `once`) that scopes one Once installation - its proxy, apps, volumes, and supporting containers - so multiple installations can coexist on one host.

**Application**:
A self-hosted web app that Once manages, defined by its image, host, and settings, and run as one or more app containers.
_Avoid_: service, deployment.

**App Profile**:
The small set of per-app deployment facts Once cannot derive from the image - base name, data path, health check path, URL env var - carried on the catalog entry, settable by flag, and persisted in the app's `once` label. The zero value means the built-in (Rails-shaped) behaviour, so apps from before App Profiles existed behave unchanged.
_Avoid_: manifest, app config - a Profile is four facts, not a configuration subsystem.

**App Container**:
The Docker container running an Application, named `{namespace}-app-{appName}-{randomID}`. More than one may run briefly for the same Application during a zero-downtime roll.

**Random ID**:
The random 6-character hex suffix on an app container's name that lets a replacement container boot alongside the old one without a naming collision. It is generated before the container exists, so it is unrelated to Docker's container ID.
_Avoid_: short ID; container ID - the suffix is not a truncation of Docker's container ID (that truncation is a distinct value, `shortContainerID`, used as the proxy routing target).

**Proxy**:
The single kamal-proxy container (`{namespace}-proxy`) that terminates TLS and routes inbound traffic to app containers by host - and, for non-Public routes, only from a Trusted Source.
_Avoid_: load balancer, gateway, router.

**App Name**:
An Application's base identity, derived from its image reference unless overridden. Addresses under every Exposure derive from it.

**Host**:
The name an Application answers to, interpreted by its Exposure: `{app name}.localhost` under Local, `{app name}.{tailnet suffix}` under Tailnet whatever the Tailnet Topology, a real DNS FQDN under Public. Derived from the App Name when not given - except under Public, where it is required.
_Avoid_: domain, URL, public hostname.

## Exposure

**Exposure**:
The single, explicitly chosen reachability level of an Application. Exactly one per Application at a time; never derived from other settings.
_Avoid_: visibility, access mode.

**Local (exposure)**:
Reachable only from the machine Once runs on: the Proxy answers its Host only to a Trusted Source. Not a LAN level - a LAN client wanting the app uses Tailnet.

**Trusted Source**:
Where a request may come from for the Proxy to answer a non-Public route: the machine Once runs on, or a container on Once's own network (a tailnet node under the Nodes Topology). Anything else gets the same not-found answer as an unknown Host. On Docker Desktop and rootless Docker the Proxy cannot see real source addresses, so Local/Tailnet enforcement degrades to Host routing there.
_Avoid_: allow-list, whitelist, internal network.

**Tailnet (exposure)**:
Reachable only from the tailnet, at `https://{app name}.{tailnet suffix}`. How the app is advertised there is the Tailnet Topology's business, not the Exposure's.

**Public (exposure)**:
Reachable from the public internet via the Proxy with TLS.

## State

**Configuration**:
The settings describing what Once has set up - apps, hosts, TLS, ports, encryption keys - stored as JSON in the `once` label on containers and volumes, never in a separate file. Distinct from Application Data. In code each component's configuration is a `…Settings` struct.
_Avoid_: config file.

**Application Data**:
An app's own persistent contents (databases, uploads, and so on), living in its Application Data Volume. Once provides the volume but has no opinion on what's inside.
_Avoid_: storage, state.

**Application Data Volume**:
The Docker volume (`{namespace}-app-{appName}`) mounted into the app container at `/storage` and `/rails/storage`, persisting Application Data across container replacements. Its `once` label also holds the app's generated encryption keys (secret key base, VAPID pair).
_Avoid_: storage volume, data dir.

**`once` label**:
The Docker label, keyed `once`, that holds a component's Configuration as JSON - app settings on the app container, proxy settings on the proxy, encryption keys on the volume.
A binary never destroys label fields it does not model: unknown fields are carried through every rewrite, and only fields explicitly deleted from the schema (the removed-field denylist) are shed - the preservation doctrine, established by commit 3e47d59 and generalized by ADR-0008.

**Label Codec**:
The one module (`internal/docker/label.go`) that round-trips a `…Settings` struct through the `once` label: key, JSON encode/decode, unknown-field preservation, removed-field denylist. Every settings type goes through it; none hand-writes its own marshalling (ADR-0008).

## Lifecycle operations

**Deploy**:
Install and start an Application for the first time - pull the image, create the volume, create the app container, and register its host with the proxy.

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
Bring up a supporting container (Proxy, Admin, the Tailnet Provider's containers) idempotently. Distinct from Deploy, which is for Applications.
_Avoid_: start.

**Start / Stop**:
Resume or pause an existing app container without recreating it. Distinct from Boot and Deploy.

**Backup / Restore**:
Capture an Application's Configuration and Application Data as a plain tar archive - exported to a file the user names, or snapshotted into the install's Backup Repos by a Backup Run - and recreate the Application from a file or a snapshot. Archives from the earlier `.tar.gz` model stay restorable.

## Backups

**Backup Repo**:
A restic repository holding the install's snapshots, one per Backup Run per participating Application, tagged `app:<name>` and `image:<ref>` - the only metadata a restore can read before it has an archive to open. Every install with backups has a local Backup Repo on host disk and, optionally, an Offsite Copy. Its settings ride the Proxy's `once` label (ADR-0014).
_Avoid_: bucket, backup directory - the repo is encrypted restic state, not a folder of archive files.

**Offsite Copy**:
The optional second Backup Repo at a remote restic URL, written by its own `restic backup` in every Backup Run - never `restic copy`, so the two repos share no locks, disk, or failure modes. Its failure logs without failing the run; its staleness is surfaced instead.
_Avoid_: mirror, replica - nothing copies from the local repo.

**Backup Run**:
The install-wide scheduled operation: for each participating Application that is due, pause, write the Staging File, unpause, and back it up to each Backup Repo; then one `forget --prune` and one `check` per repo. Runs before auto-update so the newest snapshot is of the known-good version.
_Avoid_: backup job, per-app backup - `restic backup` is per-Application, but the run, retention, and check are per-install.

**Staging File**:
The plain-tar archive written to the Staging Directory (`<local repo path>.staging`) during the pause window, read by both backup legs, then deleted. Its in-container path `/staging/<app>.tar` is fixed forever - restic groups retention by it.

**Repo Password**:
The user-held secret (32 chars, base32) encrypting both Backup Repos. Generated when backups are configured, handed over exactly once through the custody flow, cached in the Proxy label so unattended runs work. Once cannot recover it; losing it loses the backups, by design.
_Avoid_: passphrase, encryption key.

**Recovery Kit**:
Everything bare-metal restore needs, shown at the custody moment: the Repo Password, the offsite repo URL, the backend credential names (never their values), and the local repo path.

## Tailscale

**Tailnet**:
A private virtual network built using Tailscale.

**Magic DNS**:
Tailscale's automatic name registration and resolution service that assigns a unique, secure domain name to each node in a tailnet.

**Tailscale Service**:
Tailscale's first-party primitive for exposing a service on the tailnet under a stable `svc:` name with its own Magic DNS entry, advertised by a host node rather than registered as a device of its own.

**Tailnet Topology**:
The install-wide shape of how Applications reach the tailnet - Services or Nodes - chosen explicitly at enable and always recorded in Configuration. Named by shape, never by the software that implements it (ADR-0015).
_Avoid_: provider (that is the software), mode (that is host/sidecar, inside Services).

**Services (topology)**:
One shared tailnet node advertises a Tailscale Service per Application; traffic goes straight to the App Container. Implemented by Docktail. Costs one Service per app.

**Nodes (topology)**:
One tailnet node per Application, forwarding to the Proxy, which routes by Host. Implemented by TSDProxy. Costs one tagged device per app. The default for a fresh enable, and the only shape headscale can carry.
_Avoid_: per-node mode, tsnet mode.

**Tailnet Provider**:
The software behind a Tailnet Topology and the containers Once boots for it: Docktail (plus the sidecar) for Services, TSDProxy for Nodes. Exactly one is active per install; its container's existence is what "Tailscale enabled" means.

**Docktail**:
A single utility container (`once-docktail`) that watches Docker labels and advertises labelled containers as Tailscale Services through one real `tailscaled` - the unprivileged `once-tailscale` sidecar Once boots by default, or the host's own daemon when the user explicitly opts in. The Tailnet Provider for the Services topology.

**TSDProxy**:
A single container (`once-tsdproxy`) that watches Docker labels and joins one embedded tailnet node per labelled Application, forwarding to the Proxy. The Tailnet Provider for the Nodes topology.
_Avoid_: Tailscale Docker Proxy.

**Runtime Credential**:
The single tagged OAuth client the user creates directly in the Tailscale admin console and supplies when enabling Tailscale - still the only Tailscale credential that ever exists. Stored and used for ongoing Tailscale operations, including reading the Tailnet Policy (ADR-0017). Its secret reaches Once only through the environment, never on the command line (ADR-0013); Once does not protect it at rest.
_Avoid_: bootstrap credential, policy credential - the minting hierarchy they named was removed by ADR-0006.

**Tailnet Policy**:
The tailnet-wide HuJSON document Tailscale holds, defining tags, groups, grants, node attributes, and auto-approvers. Once reads it and never writes it (ADR-0017): the user is always the one who applies a change.
_Avoid_: ACL, ACL file - the document carries far more than access rules.

**Policy Snippet**:
The static per-topology HuJSON Once prints for the user to merge by hand. The only form available before the Runtime Credential exists, and the fallback whenever Once cannot read the Tailnet Policy.

**Merged Policy**:
The user's own Tailnet Policy with Once's missing sections added, generated at enable, validated through the Tailscale API before it is shown, and applied by the user. Distinct from the Policy Snippet, which is a fragment Once writes blind.
_Avoid_: policy patch, generated policy - the artifact is the user's whole document, not a fragment of ours.

**Tailscale Funnel**:
A Tailscale feature that routes public internet traffic to a specific port on a tailnet node, enabling temporary public access.

**Mode Resolution**:
The enable-time rule (ADR-0007) turning a requested Tailscale mode into the effective one - explicit wins, empty keeps the stored mode, fresh enables default to the sidecar. Exists only at enable time and only under the Services topology; one implementation (ADR-0010).

**Daemon Binding**:
Mapping a resolved or stored mode to the Tailscale Daemon that serves it - host CLI for host mode, docker exec into the sidecar otherwise. Services topology only; one function, so it must not be duplicated (ADR-0010).

**Session (Tailscale)**:
The per-operation resolution every Tailscale read path starts with - load the stored settings, bind the Tailnet Reader they imply, hand both down. Distinct from Mode Resolution: sessions never see a requested mode.

**Control Seam**:
The hidden headscale env-var pair (pre-minted auth key + control URL) that replaces SaaS OAuth so integration tests run against headscale (ADR-0002). Read through one accessor only; invisible outside `internal/docker`. Its `ONCE_TAILSCALE_` prefix is shared with one public variable (ADR-0013), so the prefix alone does not mark a variable as seam-only.

**Tailnet Reader**:
How Once reads tailnet state (Magic DNS suffix, node status, what is advertised, which Funnels are live) from the active Tailnet Provider - host CLI or sidecar docker exec under Services, TSDProxy's REST API under Nodes. Keyed by app name, never by a provider's serve-config detail. One of the Tailscale subsystem's two seams to the outside world.
_Avoid_: daemon seam, TailscaleDaemon - under Nodes there is no daemon.

**SaaS Seam**:
`tailscaleAPI` - how Once reaches the Tailscale control-plane HTTP API (OAuth exchange, key mint, Service definitions, device lists). Deliberately Tailscale-specific: a second VPN provider gets a sibling seam, not a slot in a generic provider interface (ADR-0011).

## Interfaces

**Once Admin Web App**:
A browser-based administrative interface for Once, reachable only from the tailnet. Distinct from the TUI and CLI. Its feature set is specified separately from the Tailscale integration that exposes it.
