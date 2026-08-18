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

**Status:** ready-for-agent

- [ ] `once tailscale enable` (fresh, no `--topology`) boots `once-tsdproxy`
      and no docktail/sidecar; label records `topology: nodes`
- [ ] A Tailnet app under `nodes` has a Proxy route on `{app}.{suffix}`
      (TLS off) and `tsdproxy.*` labels targeting the Proxy; under
      `services` it has no Proxy route (unchanged)
- [ ] Roll of a `nodes` app does not restart or reconfigure `once-tsdproxy`
- [ ] Deploying an app whose hostname is already a tailnet device fails with
      a message naming the device; SaaS-only, skipped on the Control Seam
- [ ] Funnel enable on a `nodes` app returns `https://{app}.{suffix}`; no
      port allocation; bounded duration and expiry unchanged
- [ ] Switching `nodes` -> `services` removes `once-tsdproxy` and
      best-effort deletes the per-app devices (unit-tested via the fake API);
      `services` -> `nodes` removes docktail + sidecar and rolls apps
- [ ] Probe/mint request is asserted byte-compatible with TSDProxy's runtime
      call (httptest); reader parsed through the production parser from a
      canned `/api/v1/proxies` fixture
- [ ] Integration (headscale seam, `flock`): default enable boots TSDProxy,
      app node joins, HTTP reaches the app via the Proxy, dashboard Traffic
      card shows requests for the tailnet app; disable removes it
- [ ] `docs/adr/0015` and CONTEXT.md need no edits; if they do, that is a
      design change - stop and report
