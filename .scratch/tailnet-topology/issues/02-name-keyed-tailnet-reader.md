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

**Status:** done

- [x] `TailscaleServeStatus` carries no container IP or proxy-target strings;
      Funnel state is keyed by app/service name
- [x] `Application.funnelBackend` is deleted; `application.go` no longer
      inspects a container for Funnel attribution
- [x] `parseServeStatus` unit test covers: funnel on, service present ->
      keyed by name; funnel on but no matching service -> not attributed;
      two apps with distinct funnels -> both keyed correctly
- [x] The "once-docktail container logs/running" hint text has one source
- [x] Integration Funnel test still passes (attribution unchanged for the
      user)

## Comments

Done on `issue-02-name-keyed-reader` (commits cb97bb5, ff713d9).

- `TailscaleServeStatus` is now `{Services, Funnels}`; `Funnels[appName] =
  host:port`. `AllowFunnel` went with `FunnelBackends`: it was dead outside
  tests once the join moved inside `parseServeStatus`.
- The join matches a service's Web handler against a funnel-enabled
  endpoint's, on the backend's `host:port` rather than the whole proxy
  target - the two sides come from separate docktail labels
  (`docktail.service.port`, `docktail.funnel.port`), and docktail compares
  its own config the same scheme-tolerant way. Checked against docktail's
  source: `buildDestination` and `desiredFunnelDestination` both yield
  `http://<container-ip>:80` under Once's labels, so the addresses match.
  Where a config offers more than one endpoint for a key, the lowest wins so
  repeated polls agree.
- `WaitForFunnelActive(ctx, host)`; `waitForFunnelBackend` -> `waitForFunnel`
  on the app name; `Application.funnelBackend` and its inspect are gone.
- The hint has one source, `Tailscale.logsHint()`, threaded into
  `serviceStatuses`, `waitForAdvertised`, `waitForApproved`, `waitForFunnel`.
  It is on the facade, not on `TailscaleDaemon`, because docktail is the
  container to name in both host and sidecar mode - the daemon adapter is the
  wrong owner. Ticket 03 moves it to the Services adapter, where ticket 04's
  adapter can name `once-tsdproxy`. Side effect: the hint is now
  namespace-derived, so a non-default namespace stops being told to check
  `once-docktail`.

Caveat on the last box: the integration Funnel test passes, but it only
exercises `Application.EnableFunnel` (the labels), never
`Namespace.EnableFunnel` / `WaitForFunnelActive` - Funnel does not exist on
the headscale control seam. So the join itself is covered by the canned-JSON
fixtures only, as the ticket intended.

Tests: `make test` ok (all `internal/...` packages); `golangci-lint run` 0
issues; `go test ./integration/ -run 'TestRemoveApplication$|Tailscale'`
ok 156.195s. The full `make integration` run had one failure,
`TestRemoveApplication`, on `pulling proxy image: ... TLS handshake timeout`
against registry-1.docker.io - the Docker Hub flake AGENTS.md describes;
`docker pull hello-world` succeeded afterwards and the test passes on re-run.
