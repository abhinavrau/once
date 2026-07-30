# PRD - Resolve Tailscale mode and daemon once

Feature slug: `tailscale-mode-daemon`. Architecture-review candidate 04.

## Problem

Three provider-shaped concepts are scattered instead of resolved once:

1. **Mode resolution** (the ADR-0007 rule: explicit mode wins, empty keeps a
   stored mode, fresh enables default to the sidecar) exists twice - in
   `effectiveMode` and again inline in `Tailscale.Enable` - kept in sync only
   by a comment ("mirroring Enable's own resolution").
2. **Daemon binding** (stored settings -> the TailscaleDaemon they imply) is
   copy-pasted as a two-line `LoadSettings` + `daemonFor` binder in five read
   paths (ServiceStatuses, ShadowWarnings, VerifyServices, WaitForFunnelActive,
   DomainSuffix), and the host-mode pre-flight binds its daemon directly with
   `NewHostTailscaleDaemon()` instead of going through `daemonFor`.
3. **The control seam** (the hidden headscale env-var pair) branches at eight
   ungathered points across three packages; two of them leak into the
   frontends, where `command` and `ui` each re-implement the same credential
   suppression, and one reads the env vars raw instead of through
   `controlSeamActive`.

## Accepted design

Each provider-shaped concept gets exactly one function - a single door, not a
provider abstraction (one adapter means a hypothetical seam):

- `resolveMode(ctx, requested) (mode, fresh, error)` is the only
  implementation of the ADR-0007 rule. `Namespace.EnableTailscale` calls it
  once, stamps the resolved mode into the settings, and `Tailscale.Enable`
  requires a resolved mode instead of re-deriving it. The "--mode host"
  fresh-enable notice moves up into `EnableTailscale`, where warnings already
  assemble (candidate-01 spine doctrine); `Tailscale.Enable` returns only an
  error.
- `session(ctx) (TailscaleSettings, TailscaleDaemon, error)` replaces the five
  read-path binders. Every daemon choice, including the host-mode pre-flight's,
  goes through `daemonFor`.
- `controlSeam() (authKey, controlURL, ok)` is the only reader of the hidden
  env vars; `controlSeamActive` delegates to it. Credential suppression under
  the seam is absorbed into `EnableTailscale`; the frontend copies and the
  `TailscaleControlSeamActive` export are deleted. Zero seam reads remain
  outside `internal/docker`.

## Decisions from grilling (evidence-settled or adopted)

- The report's single "session" node is really two concepts: enable-time mode
  resolution and per-operation daemon binding. They stay two functions;
  conflating them would give read paths a mode-resolution argument they never
  use.
- Behavior change, accepted: a corrupt or legacy label whose stored mode is
  empty, re-enabled with unchanged credentials, now resolves to sidecar and
  rebuilds the topology (stamping the mode) instead of silently ensure-running
  with a modeless label. That is ADR-0007's self-heal working on every enable;
  the state volume is kept.
- The notice rule becomes a pure helper (`fresh && requested empty && host
  socket present`), unit-testable via the ONCE_TAILSCALED_DIR override. The
  integration assertions on `Enable`'s old notice return move: the
  boots-topology test switches to `EnableTailscale` (admin-server pattern
  already exists in the suite) and asserts the notice leads the warnings; the
  host-mode test drops its notice-empty asserts, whose semantics are already
  pinned by container-identity and label asserts.
- Tag is NOT suppressed under the control seam (frontends never suppressed it);
  only ClientID/ClientSecret. The stale `TailscaleSettings.Tag` comment
  ("Empty on the headscale control seam") is corrected to "unused on".
- The four SaaS-skip branch points (`tailscale_validate.go:55,126`,
  `tailscale_status.go:43`, `tailscale_verify.go:30`) keep calling
  `controlSeamActive` unchanged - restructuring them belongs to the
  tailscale-api-adapter feature (candidate 05), which owns the SaaS HTTP seam.
- Constraint recorded in the ADR draft: **multiple VPN providers coming**.
  `resolveMode`, `daemonFor`, `session`, and `controlSeam` are the four
  provider-shaped functions; keeping each a single door keeps the future
  provider seam openable without building it now (YAGNI).

## Tickets

- 01 - Resolve the Tailscale mode once per enable
- 02 - One door for the hidden control seam (blocked by 01)
- 03 - Bind settings and daemon once per read operation (blocked by
  once-label-module)

## Cross-feature dependencies

- **once-label-module** (candidate 02): its ticket 02 blocks ticket 03 here.
  The session binder rewrites the exact `LoadSettings` call sites in
  `DomainSuffix` and `WaitForFunnelActive` whose `persistSettings`
  write-through paths (tailscale.go:357,385) that ticket moves onto the label
  codec.
- **tailscale-api-adapter** (candidate 05): no blocking edge, but this feature
  deliberately leaves the four SaaS-skip control-seam branches and all of
  `tailscale_validate.go`'s HTTP plumbing untouched for it.

## Out of scope

- Any multi-provider VPN abstraction (constraint recorded, not built).
- Restructuring the SaaS-skip branches or the baseURL/HTTP seam (candidate 05).
- The `once` label codec (candidate 02).
