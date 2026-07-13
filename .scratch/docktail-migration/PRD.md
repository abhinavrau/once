# PRD: Replace TSDProxy with Docktail (native Tailscale Services)

Status: ready-for-agent

Decided by the wayfinder map in this directory (MAP.md); go decision and
evidence trail in issues/06-go-no-go-decision.md. Research citations live in
issues/01-05, 08, 09.

> Contradicts ADR-0001 (Tailscale integration via TSDProxy) - deliberately:
> this spec supersedes it. Write a new ADR (0002) recording the Docktail /
> Tailscale Services architecture as part of implementation, and update the
> TSDProxy entry in CONTEXT.md's glossary.

## Problem Statement

Every Application exposed on the tailnet today becomes its own ephemeral
virtual Tailscale device via the TSDProxy container. For a self-hoster this
means: each app consumes a tagged-device slot on their Tailscale plan; the
admin console fills with churning ephemeral devices; Magic DNS names can
collide on re-registration; node identities need a persistent state volume
and careful ephemeral-lifecycle handling; and Once must run a loopback-only
published port just to ask TSDProxy which URLs exist. TSDProxy itself is a
community emulation of per-container devices, while Tailscale now ships a
first-party primitive (Tailscale Services, GA January 2026 on all plans) for
exactly this use case.

## Solution

Replace the TSDProxy integration with Docktail (github.com/marvinvr/docktail),
which advertises app containers as native Tailscale Services through one real
tailscaled. The tailnet sees a single device advertising `svc:` services -
one per exposed Application plus the Once Admin Web App - each with a stable
Magic DNS name computed as `https://{app}.{tailnet-suffix}`.

Once boots a `once-docktail` container and, when the host has no tailscaled
of its own, an unprivileged `once-tailscale` sidecar (userspace networking:
no TUN device, no NET_ADMIN, no host networking). `once tailscale enable`
validates credentials, offers to apply the required tailnet policy sections
itself via the Tailscale API (diff + confirm, copy-paste snippet as
fallback), and verifies services are advertised and approved. This is a
clean break: upgrading Once removes the TSDProxy integration, and users
re-run `once tailscale enable` to move to Services.

## User Stories

1. As a self-hoster, I want `once tailscale enable` to expose my apps as
   Tailscale Services from a single tailnet device, so that my apps stop
   consuming per-device slots and my admin console stays clean.
2. As a self-hoster on the free Personal plan, I want the whole integration
   to work without a paid Tailscale plan, so that self-hosting stays free.
3. As a self-hoster without Tailscale installed on my server, I want Once to
   boot an unprivileged Tailscale sidecar automatically, so that I never
   install host packages or grant a container TUN/NET_ADMIN privileges.
4. As a self-hoster who already runs tailscaled on the host, I want Once to
   detect and use it instead of booting a sidecar, so that I don't run two
   Tailscale daemons.
5. As a self-hoster, I want the enable flow to offer to apply the required
   tailnet policy sections (tagOwners, autoApprovers.services, svc: grants,
   funnel nodeAttr) via the Tailscale API after showing me a diff, so that I
   never hand-edit HuJSON in the admin console.
6. As a self-hoster who declines the API policy write (or whose write fails),
   I want the exact copy-paste policy snippet printed with instructions, so
   that I can apply it manually and re-run enable.
7. As a self-hoster who manages my tailnet policy via GitOps, I want the
   enable flow to warn me before any API policy write, so that a Once write
   isn't silently reverted by my pipeline.
8. As a self-hoster, I want the policy write to preserve my existing policy
   file byte-for-byte outside the sections Once adds - comments included -
   so that my hand-maintained ACL survives.
9. As a self-hoster, I want the policy write protected against concurrent
   edits (fail-and-retry, never clobber), so that an edit I make in the admin
   console at the same time isn't lost.
10. As a self-hoster, I want to hand the enable flow one bootstrap credential
    and have Once mint the OAuth clients it needs via the API, so that my
    admin-console work shrinks to creating a single credential.
11. As a security-conscious self-hoster, I want the powerful setup
    credentials (policy write, client minting) used once and never stored,
    with only a minimal-scope runtime credential persisted in the once label,
    so that a compromised server can't rewrite my tailnet policy.
12. As a self-hoster, I want enable to fail fast with a clear message when my
    OAuth credentials are wrong, lack a scope, or don't own the configured
    tag, so that I fix the real problem instead of debugging containers.
13. As a self-hoster, I want enable to verify each service is advertised and
    approved - and to tell me when a service is stuck pending approval with a
    pointer to autoApprovers - so that "enabled but unreachable" can't happen
    silently.
14. As a self-hoster, I want `once tailscale status` to show each exposed
    app's service URL and its advertised/approved state, so that I can see at
    a glance what is reachable on my tailnet.
15. As a self-hoster, I want app tailnet URLs shown in the CLI/TUI computed
    instantly from the stored Magic DNS suffix, so that URL display never
    waits on a live lookup.
16. As a self-hoster, I want deploying a new Application while Tailscale is
    enabled to expose it as a service automatically (and deleting it to clean
    the service up), so that tailnet exposure follows the app lifecycle with
    no extra commands.
17. As a self-hoster, I want to opt an individual Application out of the
    tailnet, so that internal-only and tailnet-exposed apps can coexist.
18. As a self-hoster, I want `once tailscale funnel enable <app> --duration`
    to keep its current semantics (bounded duration, automatic teardown via
    the background service, activation verified before success is reported),
    so that temporary public access works the same on Services.
19. As a self-hoster, I want the Once Admin Web App exposed as a Tailscale
    Service just like apps, so that the admin interface stays reachable only
    from my tailnet.
20. As a self-hoster, I want `once tailscale disable` to remove the Docktail
    container (and Once-managed sidecar) and strip service labels from my
    apps via a Roll, so that disabling leaves no tailnet exposure behind.
21. As a self-hoster upgrading Once from a TSDProxy version, I want the old
    once-tsdproxy container to stop being managed and a clear message telling
    me to re-run `once tailscale enable`, so that the clean break is explicit
    rather than silently broken.
22. As a self-hoster, I want a service name that would shadow an existing
    tailnet machine hostname to be flagged at deploy/enable time, so that my
    app URL doesn't silently resolve to the wrong node.
23. As a Once developer, I want every Tailscale cloud API request Once makes
    to be asserted byte-compatible with Docktail's and Tailscale's real
    shapes in unit tests, so that a passing probe can't diverge from real
    registration behavior.
24. As a Once developer, I want the integration suite to run without any real
    tailnet or Tailscale credentials, so that CI and outside contributors can
    run it.
25. As a Once developer, I want a minimal headscale harness test proving the
    Once-managed sidecar joins a control plane, so that the container wiring
    (socket mount, state volume, userspace mode) keeps real-control-plane
    coverage.

## Implementation Decisions

- **Topology.** One `once-docktail` container (image pinned exactly, same
  doctrine as the proxy image, bumped via Once releases). It mounts the
  Docker socket read-only and the tailscaled socket directory. The tailscaled
  comes from one of two places, decided at enable time: a detected host
  tailscaled (socket directory present on the host), else a Once-managed
  `once-tailscale` sidecar from the pinned `tailscale/tailscale` image
  running userspace networking - no TUN device, no NET_ADMIN, no host
  networking - attached to the Namespace network with a state volume
  (`once-tailscale-state`) and the socket shared to Docktail via a volume.
  Docktail's documented sidecar compose is over-privileged; Once's sidecar is
  the trimmed version research validated (issue 02).
- **Boot doctrine.** Docktail and the sidecar are supporting containers:
  Boot them idempotently, existence of `once-docktail` is the source of
  truth for "Tailscale enabled" (same doctrine as TSDProxy today), and
  Configuration lives in the `once` label on the container.
- **Labels.** App Containers carry `docktail.service.*` labels instead of
  `tsdproxy.*`: enable, name (the app name), port 80 to match Once's
  port-80 assumption. Funnel uses `docktail.funnel.*` labels. The label set
  is injected/stripped by the existing Roll retrofit on enable/disable, and
  respects the existing per-app tailnet opt-out. The Once Admin Web App's
  nginx container gets the same service labels.
- **TailscaleSettings.** Keeps ClientID, ClientSecret, Tag (with the existing
  tag normalization), and DomainSuffix. The tsdproxy lookup APIKey field and
  the loopback-published API port (127.0.0.1:8484) are deleted - Docktail has
  no local API and none is needed. New: a field recording host-vs-sidecar
  mode so status and disable know what Once manages.
- **Enable flow.** (1) Validate the runtime OAuth credentials with a probe
  byte-compatible with Docktail's real API usage (successor to the current
  mint-and-delete probe; scopes per issue 05). (2) Offer ACL provisioning via
  api-write: fetch the policy, patch only the four sections using Tailscale's
  HuJSON AST library (idempotent - skip already-present entries), show a
  unified diff plus a GitOps warning, validate server-side, then write with
  ETag If-Match; bounded retry on conflict; any failure or decline degrades
  to printing the snippet. The policy-write credential is separate,
  `policy_file`-scoped, used once, never stored. (3) Optionally accept one
  `oauth_keys`-scoped bootstrap credential and mint the runtime and policy
  clients via the API - confirm scope attenuation rules during
  implementation before exposing this path. (4) Boot sidecar (if needed),
  Docktail, and the Once Admin Web App; Roll Applications to inject labels.
  (5) Read the Magic DNS suffix from the local tailscaled and persist it in
  DomainSuffix. (6) Verify: each expected service advertised (local serve
  status) and approved (cloud services/devices endpoint, authoritative since
  local state can lag); surface pending-approval with an autoApprovers
  pointer. This replaces the log-scanning registration verification.
- **URL discovery.** Service FQDNs are deterministic: keep the existing
  TailnetURL computation (`https://{app}.{suffix}`), now fed by the suffix
  read from the local daemon at enable time instead of derived from the
  first registered node URL. No live lookup for display.
- **Local daemon access.** Everything Once reads from tailscaled (Magic DNS
  suffix, serve/services status) goes through one small interface,
  implemented with `tailscale ... --json` invocations - `docker exec` into
  the sidecar, or the host CLI in host mode. This is the single new seam.
- **Status command.** `once tailscale status` lists each exposed Application
  and the Once Admin Web App with service URL and advertised/approved/pending
  state, using the local daemon for advertisement and the cloud API (when on
  Tailscale SaaS) for approval. The registration-error log scanning of
  TSDProxy is deleted; rejection surfacing now comes from explicit API/CLI
  states.
- **Funnel.** Same user-facing semantics as today: bounded duration
  (default 10m, max 24h), background daemon required for automatic teardown,
  activation verified before success is reported. The mechanism becomes
  `docktail.funnel.*` labels applied by the same atomic container recreation,
  and activation checks move from the tsdproxy API to the local daemon /
  funnel status sources.
- **Headscale control seam.** The hidden env-var seam is kept, repointed at
  the sidecar (pre-minted auth key + control URL configure the sidecar's
  tailscaled instead of OAuth). It covers only "sidecar joins the control
  plane"; Services flows cannot run against headscale (issues 03, 09).
- **Teardown (clean break).** Delete the TSDProxy module: pinned image
  constant, YAML config template and copy-in, lookup API client, log
  scanning, loopback port binding, and the tsdproxy labels helper. Upgraded
  installs with a running once-tsdproxy get a clear "re-run `once tailscale
  enable`" message; Once does not migrate or remove the old container
  automatically, except that Destroy (full namespace teardown) removes both
  old and new Tailscale containers and volumes.
- **Vocabulary.** CONTEXT.md's TSDProxy glossary entry is replaced by a
  Docktail entry (avoid: TSDProxy, Tailscale Docker Proxy) plus a Tailscale
  Service entry; ADR-0002 records the new architecture and marks ADR-0001
  superseded.

## Testing Decisions

- Good tests here assert external behavior at the two agreed seams, never
  Docktail's or tailscaled's internals: given these settings and this fake
  API/daemon state, Once creates these containers/labels, prints these URLs
  and states, and sends exactly these API requests.
- **Seam 1 (existing pattern): httptest baseURL parameterization** for every
  Tailscale cloud API call (OAuth token, key/client minting, services and
  service-devices reads, policy GET/validate/POST with ETag handling).
  Requests are asserted byte-compatible with the real API shapes, exactly as
  the current credential-probe unit tests do - that suite is the prior art.
  Policy-write tests cover: comment-preserving patch, idempotent re-run,
  412 retry, validation failure fallback.
- **Seam 2 (new): the local-daemon interface**, faked in unit tests with
  canned `status --json` / serve-status JSON to drive suffix persistence,
  advertised/approved/pending rendering, and funnel activation waits.
- **Integration suite** (Docker required, no tailnet): enable/disable
  lifecycle creates and removes the expected containers, volumes, and
  `docktail.*` labels; Roll retrofit injects and strips labels; per-app
  opt-out respected; the TUI enable form driven in tab order - its
  integration driver must be updated in the same change as any form-field
  change (existing suite note).
- **Headscale harness**: one minimal test - the Once-managed sidecar joins
  the headscale control plane via the hidden seam. The existing headscale
  registration tests for tsdproxy are deleted with the module.
- Prior art: the existing credential-probe httptest tests, the tsdproxy unit
  tests (settings marshalling, tag normalization - these carry over), and
  the existing tailscale integration and UI-driver tests.

## Out of Scope

- Migration tooling for existing TSDProxy installs (clean-break decision):
  no relabeling, no automatic tsdproxy removal on upgrade, no settings
  carry-over.
- Implementing the map's build itself beyond this spec (the wayfinder map's
  destination ends here).
- Services support on headscale (upstream gap; the harness covers sidecar
  join only).
- API policy writes for GitOps-managed tailnets (warning + snippet only;
  GitOps management is undetectable via API).
- UDP services, non-port-80 upstreams, or multiple services per Application
  (Once's port-80 assumption stands).
- Backup/restore changes: Tailscale settings remain excluded exactly as they
  are today.

## Further Notes

- Pin down during implementation, before exposing the bootstrap-credential
  path: whether an API-created OAuth client's scopes must be a subset of its
  creator's (scope attenuation). If unattenuated, the bootstrap flow is
  strictly optional and the per-client prompt remains the default.
- The tailscale/tailscale sidecar requires v1.86.0+ for Services; pin a
  current version.
- Service names must be valid DNS labels and must not collide with tailnet
  machine hostnames (a machine's Magic DNS entry shadows a service at the
  same FQDN) - validate at enable/deploy and warn on shadowing.
- Tailscale has announced future plan-based Service limits with a legacy
  exception for existing usage; no action now, but worth a release-notes
  mention if limits ship.
