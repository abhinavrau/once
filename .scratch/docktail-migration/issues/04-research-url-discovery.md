# Research: service URL discovery without tsdproxy's REST API

Status: done
Labels: wayfinder:research
Blocked by: (none)

## Question

How should Once discover the FQDNs of docktail-advertised services for display
in the CLI/TUI, given docktail exposes no local status API? Specifically:

- Confirm service FQDN determinism: is a Tailscale Service reachable at
  `{service-name}.{tailnet}.ts.net` exactly, and how does the `svc:` prefix
  map to DNS?
- Best host-side source for the tailnet DNS suffix: `tailscale status --json`
  via the local socket, LocalAPI, or the cloud API? Once already has
  groundwork in .scratch/tailscale-integration/issues/15-fetch-tailnet-domain-suffix.md.
- Is there any way to confirm a service is actually advertised/approved
  (vs merely labeled) from the host - e.g. `tailscale serve status`,
  LocalAPI, or the cloud API's services endpoints (docktail's client uses
  /api/v2/tailnet/{t}/services, see /tmp/docktail/tailscale/client.go)?

Context: Once currently reads tsdproxy's /api/v1/proxies on 127.0.0.1:8484 to
show registered URLs (docs/adr/0001-tailscale-integration.md); that endpoint
disappears with docktail. Discovery also feeds whatever replaces the current
registration probe and rejection surfacing.

## Resolution

### 1. Service FQDN determinism: yes

A Tailscale Service named `svc:{name}` is reachable at exactly
`{name}.{tailnet-suffix}` (e.g. `https://web.tail1234.ts.net`). The `svc:`
prefix is control-plane notation only; it is stripped for DNS. The KB shows the
canonical form `https://web-server.<tailnet-name>.ts.net:443/`, and each
Service gets its own stable VIP address pair plus MagicDNS name, independent of
which host advertises it. So the URL is fully computable from
service name + tailnet DNS suffix; no runtime lookup is needed to display it.

Caveats that gate reachability (not the name):
- The name must be a valid DNS label (`svc:<dns-label>` format).
- The service name must not collide with any machine hostname on the tailnet;
  the machine's MagicDNS entry shadows the service at the same FQDN.
- The URL only resolves/serves once the service is defined, a host advertises
  it, and that host is approved (auto-approvers or manual). HTTPS additionally
  requires the tailnet's HTTPS certificates feature.

### 2. Best host-side source for the tailnet DNS suffix: local tailscaled status

Use the local daemon that docktail already depends on: `tailscale status
--json` and read `.CurrentTailnet.MagicDNSSuffix` (the top-level
`MagicDNSSuffix` field is its deprecated alias). Per ipnstate docs the field
"should be populated regardless of whether a domain has MagicDNS enabled" and
has no surrounding dots. This is LocalAPI (`/localapi/v0/status` on the
tailscaled unix socket) under the hood; from Go, Once can call it directly with
tailscale.com/client/local against the socket (host tailscaled) or run
`tailscale status --json` via `docker exec` in the sidecar topology.

Why not the alternatives:
- Cloud API: there is no endpoint that returns the MagicDNS suffix directly;
  you can only derive it by stripping the host label off a device `name` from
  /api/v2/tailnet/{t}/devices, which needs extra scope and a registered device.
  Issue 15's ephemeral mint-and-probe workaround existed precisely because
  tsdproxy had no persistent node to ask. With docktail there is always a
  persistent host/sidecar node, so that probe is obsolete: read the node's own
  status instead. Zero credentials, zero cloud dependency.
- The local source also works on the headscale control seam (headscale sets a
  base domain and the daemon reports it), where a SaaS API call must be skipped.

Keep issue 15's persistence shape: store the value in
`TailscaleSettings.DomainSuffix` at enable time and build
`https://{service}.{suffix}` for display.

### 3. Confirming advertised vs approved: yes, two layers

- Local advertisement: `tailscale serve status --json` returns a `Services`
  map keyed by `svc:{name}` with TCP/Web handler config. This is exactly what
  docktail parses (/tmp/docktail/tailscale/service.go, GetCurrentServices) and
  confirms this host has configured and advertised the endpoint. It does not
  prove approval.
- Approval (authoritative): the cloud API.
  `GET /api/v2/tailnet/{t}/services/{svc:name}/devices` returns
  `hosts[] {stableNodeID, approvalLevel, configured}` (docktail's
  listServiceHosts in /tmp/docktail/tailscale/client.go). An empty list means
  nothing advertises the service; `approvalLevel` distinguishes approved from
  pending. `GET .../services/{svc:name}` confirms the definition exists
  (defined-but-unadvertised is detectable by combining both).
  `POST .../services/{svc:name}/device/{nodeID}/approved` can even set it.
- Local approval signal exists (`tailscale status --json` ->
  `.Self.CapMap."service-host"` per KB 1552) but tailscale/tailscale#18821
  shows the daemon can miss approval changes until the service is cleared and
  re-advertised, so treat local state as best-effort and the cloud API as the
  source of truth for approval.

### Recommended discovery mechanism for Once's CLI/TUI

1. On enable, read `.CurrentTailnet.MagicDNSSuffix` from the local tailscaled
   (LocalAPI socket directly when using host tailscaled; `docker exec tailscale
   status --json` for the sidecar) and persist it as
   `TailscaleSettings.DomainSuffix`. Fail enable if the daemon is not up; this
   replaces both the tsdproxy URL readback and issue 15's ephemeral probe.
2. Display URLs by pure computation: app service `svc:{app}` ->
   `https://{app}.{suffix}`. Validate at label time that `{app}` is a DNS label
   and warn if it equals the host/sidecar node hostname (shadowing).
3. For the replacement of the registration probe and rejection surfacing, use
   a two-layer status check per app: `tailscale serve status --json` for
   "advertised locally", and (SaaS only, Once already holds OAuth credentials)
   `GET /api/v2/tailnet/{t}/services/{svc:name}/devices` for "approved vs
   pending approval". Surface "pending approval" with a pointer to
   autoApprovers when advertised but no approved host. Skip the cloud layer on
   the headscale seam.

### Sources

- https://tailscale.com/kb/1552/tailscale-services (service URL form, states,
  approval, CapMap service-host check)
- https://tailscale.com/kb/1589/tailscale-services-configuration-file
  (`svc:<service-name>` format)
- https://pkg.go.dev/tailscale.com/ipn/ipnstate (CurrentTailnet.MagicDNSSuffix,
  MagicDNSSuffix deprecation, Self.CapMap)
- https://tailscale.com/docs/features/tailscale-services and
  https://tailscale.com/blog/services-beta (svc:<dns-label>, tagged-host
  requirement, hostname shadowing, per-service VIP + MagicDNS name)
- https://github.com/tailscale/tailscale/issues/18821 (daemon lags on approval
  propagation; cloud API authoritative)
- /tmp/docktail/tailscale/client.go (listServices, listServiceHosts with
  stableNodeID/approvalLevel/configured; services API paths) and
  /tmp/docktail/tailscale/service.go (`tailscale serve status --json` parsing)
- /tmp/docktail/docs/03-tailscale-admin.md and 06-reference.md (approval flow,
  auto-approvers, credentials optional for local advertisement)
- /home/arau/code/once/.scratch/tailscale-integration/issues/15-fetch-tailnet-domain-suffix.md
  (DomainSuffix persistence shape; probe approach now superseded)
- /home/arau/code/once/docs/adr/0001-tailscale-integration.md (current tsdproxy
  lookup being replaced)
