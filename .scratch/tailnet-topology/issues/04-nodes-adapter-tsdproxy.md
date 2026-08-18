# 04 - Nodes adapter: TSDProxy per-node via the Proxy

**What to build:** The second adapter behind ticket 03's seam: one tailnet
node per Application, implemented by TSDProxy pinned to v2.3.4
(`almeidapaulopt/tsdproxy`, per-node tsnet, per-node Funnel). Container
`once-tsdproxy`, Docker socket read-only, data volume for tsnet state
(`once-tsdproxy-data`), on the namespace network, admin API bound to
loopback only. Consult the TSDProxy docs and source before claiming label or
config names; the enable-time probe must be byte-compatible with the OAuth
key-mint call TSDProxy itself makes at runtime.

Traffic goes via the Proxy: each app's `tsdproxy.*` labels name the app's
hostname (= App Name) and target `once-proxy` port 80 with the inbound Host
preserved (explicit target, autodetect off), never the app container. Under
`nodes`, `registerRouting` for a Tailnet app registers a Proxy route on
`{app}.{suffix}` with TLS off (TSDProxy terminates TLS with its Tailscale
cert), instead of removing the route. Services keeps the remove. `URL()`
stays `https://{app}.{suffix}`. Zero-downtime Roll: the app container swaps
behind the Proxy route; TSDProxy is not touched. Traffic and Visitors cards
now see tailnet apps because kamal-proxy serves them - assert it.

Adapter operations: `boot`/`remove`/`destroy` (container + volume; `destroy`
also best-effort deletes the per-app tailnet devices via the Runtime
Credential's `devices:core` scope); `appLabels`; `probe` (OAuth key-mint
shape); `reader` (TSDProxy REST `/api/v1/proxies` for what is advertised and
which Funnels are live, keyed by app name; Magic DNS suffix from the first
joined node's status); `verify` (every expected node joined and its proxy
up); Funnel: every app may Funnel on 443, URL is the app's own tailnet URL,
no port allocation; `validateHost` (Magic DNS hostname rules) plus a
deploy-time refusal when a device with that hostname already exists in the
tailnet (SaaS devices list; skipped on the Control Seam) - the Host must
equal `{app}.{suffix}`, no `-1` alias; static facts (scopes `auth_keys`,
`devices:core`; policy snippet: `tagOwners` + funnel `nodeAttr`; no mode
choices; setup intro). `--mode` with `--topology nodes` is rejected by
validation.

Once Admin Web App is exposed the same way: an `once-admin` node.

Control Seam: TSDProxy joins headscale with the pre-minted key and control
URL (its `controlUrl` + auth key config); no OAuth. Under the seam TLS is
absent (no `tailscale cert`), so the integration test asserts HTTP
reachability of an app through its node -> Proxy path, not HTTPS. TCP
passthrough with a Proxy-held certificate is out of scope (ADR-0015).

**Blocked by:** 03. Merges with 03.

**Status:** done

- [x] `once tailscale enable` (fresh, no `--topology`) boots `once-tsdproxy`
      and no docktail/sidecar; label records `topology: nodes`
- [x] A Tailnet app under `nodes` has a Proxy route on `{app}.{suffix}`
      (TLS off) and `tsdproxy.*` labels targeting the Proxy; under
      `services` it has no Proxy route (unchanged)
- [x] Roll of a `nodes` app does not restart or reconfigure `once-tsdproxy`
- [x] Deploying an app whose hostname is already a tailnet device fails with
      a message naming the device; SaaS-only, skipped on the Control Seam
- [x] Funnel enable on a `nodes` app returns `https://{app}.{suffix}`; no
      port allocation; bounded duration and expiry unchanged
- [x] Switching `nodes` -> `services` removes `once-tsdproxy` and
      best-effort deletes the per-app devices (unit-tested via the fake API);
      `services` -> `nodes` removes docktail + sidecar and rolls apps
- [x] Probe/mint request is asserted byte-compatible with TSDProxy's runtime
      call (httptest); reader parsed through the production parser from a
      canned `/api/v1/proxies` fixture
- [x] Integration (headscale seam, `flock`): default enable boots TSDProxy,
      app node joins, HTTP reaches the app via the Proxy, dashboard Traffic
      card shows requests for the tailnet app; disable removes it
- [x] `docs/adr/0015` and CONTEXT.md need no edits; if they do, that is a
      design change - stop and report

## Comments

Implemented on branch `issue-03-04-topology-seam`, on top of ticket 03's
seam. New files: `internal/docker/tailscale_nodes.go` (the `nodesTopology`
adapter, config generation, probe, device-collision guard, best-effort
device cleanup), `internal/docker/tailscale_nodes_reader.go` (the TSDProxy
REST `TailnetReader`), `internal/docker/tailscale_nodes_test.go`,
`integration/tailscale_nodes_test.go`. `internal/docker/tailscale_policy.go`
gained `NodesPolicySnippet`.

**TSDProxy facts used, with doc reference** (v2.3.4,
almeidapaulopt.github.io/tsdproxy and the source tree at tag `v2.3.4`):

- Image `almeidapaulopt/tsdproxy:2.3.4` (Docker Hub tag has no `v` prefix,
  unlike the git tag).
- Container labels (`internal/targetproviders/docker/consts.go`,
  `docs/content/docs/providers/docker.md`): `tsdproxy.enable`,
  `tsdproxy.name` (the node's tailnet hostname), `tsdproxy.port.<n>` with
  the `<proxy>/<proto>:<target>/<proto>[, options]` syntax and the
  `no_autodetect`/`tailscale_funnel` port options.
- Config file only, no env vars for OAuth/tags/target host in this version
  (`internal/config/generateproviders.go` lists the only legacy env vars
  still read: `DOCKER_HOST`, `TSDPROXY_HOSTNAME`, `TSDPROXY_AUTHKEY`,
  `TSDPROXY_AUTHKEYFILE`, `TSDPROXY_CONTROLURL`, `TSDPROXY_DATADIR` - none
  of them cover `clientId`/`clientSecret`/`tags`/`targetHostname`). Once
  writes `/config/tsdproxy.yaml` (`internal/config/config.go`'s schema)
  with one Docker provider (`targetHostname: once-proxy`, so every app's
  `no_autodetect` port resolves straight to the Proxy regardless of which
  container carries the label - `getTargetURL`'s `resolvePublished` path,
  `internal/targetproviders/docker/container.go`) and one Tailscale
  provider (OAuth or the Control Seam's auth key + control URL, tagged,
  never both).
- Admin API (`docs/content/docs/operations/api.md`,
  `docs/content/docs/security/admin-allowlist.md`): `GET /api/v1/proxies`
  is the reader's one source; `adminAllowLocalhost` is written explicitly
  rather than relied on as the auto-Docker default, and the API is
  published to `127.0.0.1` only (host loopback, port 8180) - Once's own
  process reads it the same way it reads kamal-proxy's metrics port, not
  over the Docker network.
- OAuth key mint (`internal/proxyproviders/tailscale/provider.go`'s
  `getOAuth`, `internal/model/proxyconfig.go`'s `Tailscale` struct): one
  key per app node, `reusable: false`, `ephemeral: false`,
  `preauthorized: true`, tagged, `description: "tsdproxy"`. The probe
  mints (and deletes) exactly this shape via the existing `tsKeyRequest`
  mirror ticket 03 already built for Docktail's sidecar key - same
  Tailscale API request shape, different field values.
- tsnet TLS needs a `tailscale cert` capability the Control Seam does not
  have (`internal/proxyproviders/tailscale/provider.go` construction of
  `tsnet.Server`; confirmed live: headscale rejects the HTTPS listener with
  "you must enable HTTPS in the admin panel to proceed"). `appLabels` emits
  `443/https` on SaaS and `80/http` under the Control Seam so every node
  can actually start there - this is also why the integration test asserts
  plain HTTP, per this ticket's own text.
- `CopyToContainer` needs its destination directory to already exist;
  TSDProxy's image is `FROM scratch` with no filesystem tree at all beyond
  the binary, so the config tar entry is written as `config/tsdproxy.yaml`
  under `/` (always present) rather than into `/config` directly.

**Interface widened beyond ticket 03's sketch** (same pattern ticket 03 used
for `probe`/`verify`/`status`, documented there): `remove`/`destroy` now
take `settings TailscaleSettings` (nodes needs `Tag`/credentials for its
best-effort device sweep on the way out; Services ignores it), and
`validateHost` takes `ctx`/`settings` too (nodes' live devices-list check,
skipped on the Control Seam; Services' shape-only check ignores both).
Every facade call site (`Enable`, `Disable`, `Destroy`) was updated to pass
the settings it already had in hand.

**Design decisions not spelled out in the ticket text:**

- `funnelPort(self, apps)` returns a fixed constant (443) rather than `(0,
  nil)`: nodes' Funnel is a per-app on/off with nothing to allocate, but
  `appLabels`'s `funnelPort` parameter is the existing "0 means off, nonzero
  means on" signal every call site already understands - reusing it kept
  `Application.EnableFunnel`/`AppLabels` topology-oblivious rather than
  adding a second on/off signal.
- The device-collision refusal (`validateHost`) exempts an app whose own
  TSDProxy node already advertises the requested Host (checked live against
  the reader before ever calling the SaaS devices API) - otherwise a
  redeploy or an unrelated settings change on an already-`nodes`-exposed app
  would refuse itself as a "collision" with its own device. It is
  best-effort in the failure direction too: an unreadable devices list does
  not block a deploy whose credentials already proved they could mint a
  key.
- `remove`'s best-effort device cleanup (`bestEffortDeleteTaggedDevices`)
  sweeps every device carrying the install's tag, not a specific per-app
  list - the interface has no app list to give it, and the tag is
  documented as Once's own, so a tag-based sweep is the coarse-grained
  "best effort" the ticket's own wording calls for. It never runs on the
  Control Seam (no SaaS devices API there) or without OAuth credentials.
- Added `validateModeTopologyCombination`, called from
  `Namespace.EnableTailscale` once the topology resolves: a requested
  `--mode` is rejected once the resolved topology is `nodes` (ticket text:
  "`--mode` with `--topology nodes` is rejected by validation"). The CLI has
  no `--topology` flag yet (ticket 05), so this can only be reached today by
  a fresh enable that resolves to nodes while also passing a Mode, or once
  ticket 05 wires the flag through - implemented now so it is in place
  before that lands.
- once-admin is exposed the same way an app is under nodes (a Proxy route
  on `once-admin.{suffix}`, TLS off): `admin.go`'s `create` gained the same
  `registerNodesProxyRoute` call `application.go`'s `registerRouting` uses,
  factored into one shared helper. Like the pre-existing Services behavior,
  this only happens when once-admin is freshly created - `Boot`'s
  already-exists path (`ensureRunning`) never re-registers a route, so
  enabling Tailscale after once-admin already existed (e.g. via the
  recovery handoff) still won't retrofit it. That gap predates this ticket
  and is not nodes-specific; not fixed here.
- `ResolveHost` (`exposure.go`) was **not** touched: the DNS-label shape
  check it does is identical for both topologies (Magic DNS naming is
  common), so ticket 03's flagged item was really about the *device*-
  collision check, which only makes sense at deploy time with live API
  access - that lives in `Application.validateTailnetExposure` via the new
  `Tailscale.ValidateHost` facade method, not in the ctx-free `ResolveHost`.
- `TailscaleSettings.FunnelURL(port)` (Services-shaped, NodeHost-based) is
  still untouched, per ticket 03's note - nothing in the nodes path calls
  it. The generic facade plumbing (`WaitForFunnelActive` -> `waitForFunnel`
  -> `funnelURL`) already produces the right `https://{app}.{suffix}` for
  nodes because the reader's `ServeStatus().Funnels[name]` reports
  `"{app}.{suffix}:443"` directly - no topology-specific Funnel URL method
  was needed after all.
- Left for ticket 05: `internal/command/tailscale.go` and
  `internal/ui/tailscale_form.go` still pin `Topology:
  docker.TailnetTopologyServices` explicitly on every enable, so today's
  CLI/TUI behavior is unchanged rather than silently landing on `nodes`.
  The "fresh enable, no `--topology`" checklist item is proven through the
  facade/integration harness directly (`ns.EnableTailscale` with
  `Topology: ""`), per this ticket's framing. `topologyFacts` (scopes,
  policy snippet, setup intro, `ModeChoices: nil`) is implemented and
  correct but unreached by any frontend until ticket 05 wires a selector to
  it - same status ticket 03 left Services' `facts()` in.
- Once-admin's `AppLabels`/route registration, and TSDProxy's admin port
  (fixed at 8180, loopback), are not currently configurable per-namespace -
  two Once installs on the same host would collide on that port. This
  matches the existing precedent (kamal-proxy's own ports are fixed
  defaults too, requiring explicit customization for a second install) and
  was not expanded on, per the ticket's scope.

**Test results:**

- `make build`: clean.
- `gofmt -l .`: clean.
- `golangci-lint run ./...`: 0 issues.
- `make test` (`go test ./internal/...`): all packages pass (353 tests in
  `internal/docker` alone, including this ticket's 33 new unit tests:
  label generation, `buildConfig` YAML content and Control Seam variant,
  the probe's byte-compatible mint request via both the fake seam and a
  real `httptest.Server` through the production `httpTailscaleAPI`, the
  reader's parser fed a canned `/api/v1/proxies` fixture and via a real
  HTTP round-trip, `validateHost`'s collision/exemption/best-effort paths,
  `deleteTaggedDevices`'s tag-filtered sweep, and
  `validateModeTopologyCombination`).
- `go test ./integration/ -run 'Tailscale' -count=1 -timeout 30m`: all 8
  tests pass, including three new ones -
  `TestTailscaleEnableBootsNodesTopology` (fresh enable defaults to nodes,
  once-admin and an app node both join headscale, the Proxy route appears
  on `{app}.{suffix}` with TLS off, a request shaped like TSDProxy's own
  forward reaches the app, kamal-proxy's Prometheus metrics show it, a Roll
  leaves `once-tsdproxy` untouched, Funnel toggles the port label, disable
  removes tsdproxy and keeps its data volume) and
  `TestTailscaleRoundTripsBetweenNodesAndServices` (an explicit
  `--topology` switch each way: nodes -> services tears down tsdproxy and
  boots docktail/the sidecar with the app rolled onto docktail labels;
  services -> nodes tears those down and boots tsdproxy again, the app
  node rejoining via the kept data volume).
- `make integration` (full suite): run twice against this ticket's changes
  (once mid-work at 742s with 50 tests, once at the final state at 831s
  with all 51), both fully green. A pre-work baseline full run at the
  branch's starting commit (before this ticket) hit one failure under
  full-suite load, `TestVaultwardenBackupAndBareMetalRecovery` ("host
  settings conflict with another service") - unrelated to Tailscale, not
  reproduced in either of this ticket's own full runs, consistent with
  ticket 03's conclusion that full-suite failures of this shape are
  environment-driven, not caused by a specific feature.

**Not integration-tested, and why:** the device-collision refusal
(`validateHost`) is explicitly SaaS-only and skipped on the Control Seam
(this ticket's own text), so headscale cannot exercise the refusal path at
all - it is unit-tested against the fake `tailscaleAPI` instead, which is
the only level where it is exercisable in CI. Funnel's live activation wait
(`Namespace.EnableFunnel`'s `WaitForFunnelActive` polling) is untested at
the integration level for the same reason the existing Services test never
exercised it either: headscale has no Funnel at all, so both topologies'
integration coverage stops at the label/mechanism level
(`Application.EnableFunnel`), not a live-served wait.
