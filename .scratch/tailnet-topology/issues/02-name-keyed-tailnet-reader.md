# 02 - Key the Tailnet Reader by app name; delete `Application.funnelBackend`

**What to build:** Review candidate 02. The Daemon Seam (`TailscaleDaemon`)
returns Funnel state shaped like Docktail's serve config:
`FunnelBackends[http://172.x:80] = host:port`. To say "this app's Funnel is
live", `Tailscale.WaitForFunnelActive` asks `Application.funnelBackend` to
inspect the container, read its IP on the namespace network and rebuild
Docktail's proxy target, then matches. A backend IP is a serve-config detail;
it must not live on Application, and a REST-backed reader (ticket 04) cannot
produce it.

Change `TailscaleServeStatus` so Funnel state is keyed by the app (service)
name: `Funnels[appName] = host:port` (or an equivalent name-keyed shape).
The CLI-backed reader does the join once, inside `parseServeStatus`: a
service's proxy target in `Services[svc:name].Web[...].Handlers[...].Proxy`
matched against the funnel-enabled `Web[hostPort].Handlers[...].Proxy`. Then
`waitForFunnelBackend` becomes a wait on the app name; `funnelBackend` and its
inspect are deleted; `WaitForFunnelActive` takes the app's Host, not the
Application. Verify and status stay generic. The interface keeps its five
reads. Do not rename the interface yet (ticket 03 does, when the second
adapter exists).

Move the four copies of the "check that the once-docktail container is
running" wording into one place the reader adapter supplies, so ticket 04's
adapter can name its own container.

The canned-JSON `FakeTailscaleDaemon` and its serve-status fixtures are the
test surface: update fixtures to include a service whose Web handler proxies
to the same target as the funnel-enabled endpoint, and assert the join.

**Blocked by:** None - can start immediately. Independent of 01.

**Status:** ready-for-agent

- [ ] `TailscaleServeStatus` carries no container IP or proxy-target strings;
      Funnel state is keyed by app/service name
- [ ] `Application.funnelBackend` is deleted; `application.go` no longer
      inspects a container for Funnel attribution
- [ ] `parseServeStatus` unit test covers: funnel on, service present ->
      keyed by name; funnel on but no matching service -> not attributed;
      two apps with distinct funnels -> both keyed correctly
- [ ] The "once-docktail container logs/running" hint text has one source
- [ ] Integration Funnel test still passes (attribution unchanged for the
      user)
