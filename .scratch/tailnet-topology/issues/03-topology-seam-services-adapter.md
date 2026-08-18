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

**Status:** ready-for-agent

- [ ] `TailscaleSettings.Topology` is written on every enable; a stored label
      without it makes every read path return the older-Once error
- [ ] `resolveTopology` has one implementation; explicit wins, empty keeps
      stored, fresh defaults to `nodes` (unit-tested without a daemon)
- [ ] `docktailLabels` and `daemonFor` are unexported inside the Services
      adapter; `application.go`, `admin.go`, `namespace.go` reference only the
      facade
- [ ] `controlSeamActive` is called only inside the Services adapter and the
      facade; zero calls in `tailscale_validate.go`/`_verify.go`/`_status.go`
      as top-level branches
- [ ] Changing topology on an enabled install rebuilds the topology (state
      volume kept) and rolls apps, like a credential change
- [ ] Every existing `tailscale_*_test.go` passes through the facade or the
      Services adapter without new exported surface
- [ ] Integration: existing docktail/headscale tests pass with
      `--topology services` set explicitly
