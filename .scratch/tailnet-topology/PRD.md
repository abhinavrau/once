# PRD - Tailnet Topology seam: Services (Docktail) and Nodes (TSDProxy)

Feature slug: `tailnet-topology`. Architecture-review 2026-08-17 candidate 01,
with candidates 02 (name-keyed reader), 04 (frontends stop naming the
provider) and 05 (delete legacy TSDProxy cleanup) folded in. Decision record:
ADR-0015. Glossary: CONTEXT.md "Tailscale" section.

## Problem

Once reaches the tailnet one way only: Docktail advertising a Tailscale
Service per app from one shared node (ADR-0002). Two drivers make that the
wrong only-way:

1. Tailscale Services are capped on the free plan; tagged nodes are not
   (50 included). A self-hoster with a dozen apps hits the Services limit
   first.
2. headscale, which Once must be able to target later, has no Services, Serve
   or Funnel. Only plain nodes join it.

Both point at one tailnet node per application - the shape ADR-0002 replaced.
The Services shape stays right for a small install on SaaS Tailscale, so this
is not a second clean-break swap. It is the second provider ADR-0010/0011 said
the seam would wait for.

Today the Docktail specifics leak past the `Tailscale` facade: label emission
is called from `application.go` and `admin.go`, daemon dispatch from
`namespace.go`, the control seam branches in four files, `funnelBackend` on
Application derives Docktail's serve target, and the TUI/CLI type out
Docktail's modes, scopes and policy snippet by hand. Adding a second provider
means editing all of them.

## Accepted design

- **Tailnet Topology** is the axis, named by shape: `services` (one shared
  node, a Tailscale Service per app - Docktail) or `nodes` (one tailnet node
  per app - TSDProxy v2 per-node tsnet). Install-wide, always written
  explicitly into the provider container's `once` label, chosen by
  `--topology` / a TUI selector. `nodes` is the fresh-enable default.
- A **private seam** under the `Tailscale` facade, one adapter per topology.
  The facade stays concrete and deep: settings on the label,
  enabled-by-existence, rebuild-on-change, retrofit Roll, bounded Funnel
  duration, suffix/URL persistence, Control Seam accessor. Adapters own:
  boot/remove/destroy, app label dialect, enable-time credential probe,
  Tailnet Reader, verify, Funnel port allocation and URL shape, host-name
  validation, and the static facts frontends render (defaults, scopes, policy
  snippet, mode choices, setup prose).
- Mode (host/sidecar), Mode Resolution and Daemon Binding become facts of the
  Services adapter. Session and the Control Seam stay in the facade.
- **Nodes routes via the Proxy**: each app node forwards HTTP with Host
  preserved to `once-proxy`, which routes by host. Services stays direct.
  SaaS TLS only (TSDProxy holds the cert); TCP passthrough for headscale is
  the adapter's later decision.
- Under `nodes` the Host must equal `{app}.{suffix}`; an existing tailnet
  device with that hostname is refused at deploy time.
- One Runtime Credential for both topologies; per-adapter scope list and
  policy snippet; each adapter's probe is byte-compatible with what its
  provider sends at runtime.
- No backward compatibility: a label without `topology` makes read paths say
  "setup is from an older Once; re-run `once tailscale enable`". Legacy
  TSDProxy cleanup is deleted first so `once-tsdproxy` is free.
- Switching topology rebuilds and rolls; leaving `nodes` best-effort deletes
  the per-app devices through the Runtime Credential.

## Decisions from grilling

- Seam only with two real adapters: 03 (seam + Services adapter) and 04
  (Nodes adapter) merge together as one feature; 03 does not land alone.
- Axis is shape, not software. TSDProxy v3 Services mode is never a third
  adapter unless Docktail dies.
- Tailnet exposure keeps one meaning ("reachable only from the tailnet at
  `{app}.{suffix}`"); how it is advertised is the topology's business.
- Via-Proxy tailnet apps answer on the public IP to a spoofed Host header,
  same as Local apps already do. Accepted now; ticket 06 records the shared
  fix as a separate feature.
- Per-node on headscale is integration-testable through the existing Control
  Seam; the default topology gains end-to-end coverage. Docktail's headscale
  test stays as an explicit `--topology services` sidecar-join test.
- Pin TSDProxy v2.3.4 (per-node tsnet, per-node Funnel, REST + /metrics).
- Traffic/Visitors cards for tailnet apps come free under `nodes` (via
  Proxy); no traffic-source seam (review candidate 06) is built.

## Tickets

- 01 - Delete the legacy TSDProxy cleanup path
- 02 - Key the Tailnet Reader by app name; delete `Application.funnelBackend`
- 03 - Open the Tailnet Topology seam with the Services adapter (blocked by
  01, 02; merges with 04)
- 04 - Nodes adapter: TSDProxy per-node via the Proxy (blocked by 03)
- 05 - Frontends: `--topology`, per-topology facts, older-install message
  (blocked by 04)
- 06 - Non-public exposures answer to spoofed Host headers on the public IP
  (separate feature; needs-triage)
