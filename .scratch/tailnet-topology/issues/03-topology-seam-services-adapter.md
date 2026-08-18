# 03 - Open the Tailnet Topology seam with the Services adapter

**What to build:** The private seam under the `Tailscale` facade (ADR-0015),
with the existing Docktail implementation moved behind it as the Services
adapter, and the topology recorded explicitly in Configuration. Behaviour of
a Services install is unchanged except where this ticket says otherwise. This
ticket merges only together with 04 - a seam with one adapter is not merged
to main.

Settings: `TailscaleSettings` gains `Topology string` (`services` | `nodes`,
`json:"topology"`), always written. `Mode` stays but is documented as
Services-only. A `resolveTopology(ctx, requested)` rule beside `resolveMode`:
explicit wins, empty keeps the stored topology, a fresh enable defaults to
`nodes`. Read paths (`session`, `LoadSettings` consumers) fail with "the
Tailscale setup is from an older version of Once; re-run `once tailscale
enable`" when the stored label has no topology. No inference from the
container name.

Seam (private, `internal/docker`): one adapter per topology behind a small
interface the facade owns. Starting operation list, cut in review:
`boot(ctx, settings)`, `remove(ctx)`, `destroy(ctx)`; `appLabels(host,
network, funnelPort) map[string]string`; `probe(ctx, settings) error`
(enable-time credential check); `reader(settings) TailnetReader` (the
renamed `TailscaleDaemon`, ticket 02's shape); `verify(ctx, expected,
progress) error`; `funnelPort(self, apps) (int, error)` and `funnelURL(settings,
port) string`; `validateHost(name) error`; and a static facts struct
(default settings, OAuth scopes, policy snippet, mode choices, setup intro).
The facade keeps: settings on the provider container's label, `Enabled` by
existence (inspect the active/known provider container names), rebuild-on-
change (topology joins credentials/tag/mode as a rebuild trigger),
`RollApplications` after enable/disable, bounded Funnel duration,
suffix/NodeHost persistence, `session`, `controlSeam`.

Moves into the Services adapter: `create` (docktail spec), `bootSidecar*`,
`removeTopology`, `resolveMode`, `daemonFor`, `docktailLabels`,
`funnelPublicPorts` + `Namespace.allocateFunnelPort`, `NodeHost`-based
`FunnelURL`, `ValidateTailscaleServiceName`, `validateHostModePreflight`,
`validateTailscaleCredentials` (Services probe), `PolicySnippet`,
`VerifyServices`, `ShadowWarnings`. Callers that reach around the facade
today go back through it: `application.go:579` and `admin.go:122` call a
facade `AppLabels(...)`; `namespace.go:299` no longer calls `daemonFor`
(the pre-flight is inside the adapter's `probe`). The four
`controlSeamActive()` branch points (validate x2, status, verify) collapse
into the Services adapter's probe/verify/status, leaving the accessor as the
only reader.

Rename `TailscaleDaemon` to `TailnetReader` (CONTEXT.md term) now that a
second adapter is arriving; keep the fake.

**Blocked by:** 01, 02. Merges with 04.

**Status:** done

- [x] `TailscaleSettings.Topology` is written on every enable; a stored label
      without it makes every read path return the older-Once error
- [x] `resolveTopology` has one implementation; explicit wins, empty keeps
      stored, fresh defaults to `nodes` (unit-tested without a daemon)
- [x] `docktailLabels` and `daemonFor` are unexported inside the Services
      adapter; `application.go`, `admin.go`, `namespace.go` reference only the
      facade
- [x] `controlSeamActive` is called only inside the Services adapter and the
      facade; zero calls in `tailscale_validate.go`/`_verify.go`/`_status.go`
      as top-level branches
- [x] Changing topology on an enabled install rebuilds the topology (state
      volume kept) and rolls apps, like a credential change
- [x] Every existing `tailscale_*_test.go` passes through the facade or the
      Services adapter without new exported surface
- [x] Integration: existing docktail/headscale tests pass with
      `--topology services` set explicitly

## Comments

Implemented on branch `issue-03-04-topology-seam`. `TailscaleDaemon` was
renamed to `TailnetReader` first (separate commit), then the seam opened
(second commit), then a bug fix for the empty-topology self-heal path
(third commit).

**Final seam interface** (`internal/docker/tailscale_topology.go`,
`tailnetTopology`, private):

- `boot(ctx, settings) error` - idempotent create-or-ensure-running
- `remove(ctx) error` - tear down containers, keep state volume
- `destroy(ctx) error` - `remove` plus the state volume
- `relabel(ctx, settings) error` - cheap label-only rewrite (added: see
  below)
- `appLabels(host, network, funnelPort) map[string]string`
- `probe(ctx, settings) (TailscaleSettings, string, error)` - enable-time
  preflight/credential check; returns settings with topology-owned
  defaulting resolved (Services' `Mode`) and a notice string (changed: see
  below)
- `reader(settings) TailnetReader`
- `verify(ctx, settings, expected, progress) error` - added `settings`
  (see below)
- `status(ctx, settings, names) ([]ServiceStatus, error)` - added; absent
  from the starting sketch but required by "the four `controlSeamActive`
  branch points ... collapse into the Services adapter's
  probe/verify/status"
- `funnelPort(self, apps) (int, error)`
- `validateHost(name) error`
- `logsHint() string` - added, per ticket 02's note that 03 should move the
  logs hint into the Services adapter so ticket 04 can name its own
  container
- `facts() topologyFacts` - static, unexported for now (ticket 05 decides
  how frontends reach it)

Deviations from the starting sketch, and why:

- `probe` returns `(TailscaleSettings, string, error)` instead of `error`:
  mode resolution (ADR-0007) has to happen exactly once and its result
  (the resolved `Mode`, and the `--mode host` notice) has to reach the
  settings that get booted; a plain `error` return had nowhere to carry
  that back through the facade-owned `EnableTailscale`.
- `verify` takes `settings` explicitly: its cloud-side check needs
  `settings.Tag`/credentials, which a settings-free signature (matching
  `verify(ctx, expected, progress)` as sketched) can't carry once it isn't
  reached through the facade's own `session()` anymore.
- `status(ctx, settings, names)` was added (not in the starting list) to
  give `serviceStatuses`'/`ServiceStatuses`' `controlSeamActive()` check a
  home in the adapter, per the ticket's own "probe/verify/status" framing
  of the four collapse points.
- `funnelURL(settings, port) string` from the sketch was **not** added.
  `TailscaleSettings.FunnelURL(port)` (NodeHost-based) stays as a plain,
  Services-shaped method, used directly by `internal/command/tailscale.go`
  and `internal/ui/dashboard.go` with no ctx/facade access at that call
  site. Making it topology-aware needs an app name too (nodes' Funnel URL
  is `https://{app}.{suffix}`, not host:port) and touches those frontend
  call sites - real work, but it's ticket 04/05's to do when nodes actually
  needs rendering, not a `03` seam-opening concern. Flagging this explicitly
  for whoever does 04/05.
- `ValidateTailscaleServiceName`/host-name validation for
  `exposure.go`/`application.go`'s pre-deploy checks was **not** rewired
  through `validateHost`/the facade. Those call sites stay on the plain
  exported `ValidateTailscaleServiceName` function (now living in
  `tailscale_services.go`), synchronous and topology-oblivious. Making
  per-app host validation topology-aware (nodes needs `{app}.{suffix}`
  equality, not just a DNS-label shape) is real, separate work for
  ticket 04/05; wiring it in here would have meant plumbing ctx/topology
  through `ApplicationSettings` validation, well past this ticket's scope.
- `Namespace.allocateFunnelPort` **was** moved, becoming
  `servicesTopology.funnelPort`, reached through a new facade method
  `Tailscale.FunnelPort(ctx, self)` - the ticket's sketch named it, and the
  move was cheap once the seam existed.
- `Tailscale.VerifyServices` was renamed to `Tailscale.Verify` (the
  interface's generic `verify` op, not Services-specific); `ServiceStatuses`
  kept its name (it now delegates to `topology.status`).
- `ShadowWarnings` (and its private `shadowWarnings` helper) was **not**
  moved into the Services adapter, despite being named in the ticket's
  "moves into..." list. Its only topology dependency is the `TailnetReader`
  it's handed (`MachineHostnames`), which the seam already makes generic -
  there is nothing Services-specific left in it, so it stays a facade
  method (`tailscale.go`) backed by a plain helper (`tailscale_names.go`).
  Physically relocating it would have been page-shuffling, not a design
  change.
- `PolicySnippet` and its `tailscale_policy.go` helpers were **not**
  physically moved; `topologyFacts.PolicySnippet` just references the
  existing exported function. It has zero dependency on `Tailscale`/session
  state and the file has nothing else in it, so moving it bought nothing.

**`nodes` before ticket 04 exists:** `resolveTopology` really does default a
fresh enable (and a pre-ADR-0015 label's empty stored topology) to `nodes`.
`Tailscale.topologyFor("nodes")` returns a plain error - `the nodes tailnet
topology is not implemented yet` - with no Docker/API touched getting there
(see `TestEnableTailscaleNodesTopologyNotImplementedYet`). Ticket 04 replaces
that one `case` in `topologyFor` with the real adapter; nothing else in the
facade needs to change. Until ticket 05 adds a `--topology`/selector,
`internal/command/tailscale.go` and `internal/ui/tailscale_form.go` both pin
`Topology: docker.TailnetTopologyServices` explicitly on every enable, so
today's CLI/TUI behavior is unchanged rather than silently landing on the
(currently unimplemented) `nodes` default.

**Rebuild-on-topology-change:** `Tailscale.Enable` compares
credentials/tag/mode/topology (`sameTopologyRequest`) exactly as before, with
`Topology` added to the comparison; a mismatch removes the old topology's
containers (state volume kept - `remove`, not `destroy`) and boots the new
one, then rolls apps. A pre-ADR-0015 label's empty stored topology is treated
as `services` when choosing what to remove (`activeTopologyName`) - the only
shape that ever existed before this ADR - rather than erroring. An actual
`services` <-> `nodes` switch can't be exercised until ticket 04 ships the
second adapter; the mechanism is exercised today by the mode-change and
topology-self-heal rebuild integration scenarios, which take the same code
path.

A full-suite run surfaced one real bug in `admin.go`'s `create`: it assumed
`Boot` is only ever reached through the Tailscale enable lifecycle (so
`AppLabels`'s `enabled` could be hardcoded `true`), but
`recovery_handoff.go` also boots once-admin with Tailscale possibly
disabled, and `AppLabels(ctx, true, ...)` now does a real
`LoadSettings` (unlike the old pure `docktailLabels`), which errors when
Tailscale isn't enabled. Fixed by checking `Tailscale.Enabled(ctx)` live in
`create` instead of assuming `true`.

**Test results:**

- `make build`: clean.
- `golangci-lint run ./...`: 0 issues.
- `make test` (`go test ./internal/...`): all packages pass.
- `go test ./integration/ -run 'TestRecoveryHandoff|Tailscale' -count=1
  -timeout 30m`: all 8 tests pass.
- `make integration` (full suite, run twice): both runs pass everything
  except `TestTailscaleEnableBootsDocktailTopology`, which failed both
  times with "sidecar never joined the control plane" (a 90s
  require.Eventually on the headscale control plane) - but only when run
  as part of the full sequential suite. It passes cleanly (~27s, the
  normal time) every other way it was run: twice standalone
  (-run Tailscale), once alongside the recovery-handoff tests, and once
  run back-to-back right after the single heaviest preceding test
  (TestRestoreVaultwardenFromRepo) to try to reproduce the load - still
  passed. This points at cumulative resource pressure from the full
  suite's ~50 sequential Docker-backed tests, not this ticket's changes;
  not chased further per AGENTS.md's guidance on environment-driven
  flakiness.

File moves (`git mv`, contents folded into `tailscale_services.go` or
trimmed): `tailscale_validate.go`, `tailscale_verify.go`, `tailscale_status.go`
deleted (checklist item 4 needs their old names gone, not just edited);
their tests renamed to `tailscale_services_probe_test.go`,
`tailscale_services_verify_test.go`, `tailscale_services_status_test.go`,
`tailscale_services_sidecar_test.go`. `tailscale_names.go` trimmed to just
`shadowWarnings`. New files: `tailscale_topology.go` (seam interface,
`resolveTopology`, topology consts/validation, `topologyFacts`),
`tailscale_services.go` (the Services adapter and everything Services-only
that used to live in `tailscale.go`).

**Follow-up (full-suite failure was not a flake):** the two full-suite failures
of `TestTailscaleEnableBootsDocktailTopology` ("sidecar never joined the
control plane") were a real regression. The seam's `Destroy` returned early
when the stored label could not be read (docktail already removed by
`Disable`), so `Namespace.Teardown` leaked the sidecar state volume; the next
test that booted a sidecar into the same volume name inherited stale node keys
and never joined its fresh headscale. `Destroy` now always destroys, falling
back to the Services topology when the label is absent, matching the
pre-seam unconditional cleanup.
