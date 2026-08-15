# Research: configuring kamal-proxy's health check path

Status: done
Labels: wayfinder:research
Blocked by: (none)

## Question

Can Once tell kamal-proxy to probe a health path other than its default, and if
so, how?

This is the gate that decides whether Vaultwarden can be deployed at all.
Vaultwarden answers `/alive`, not `/up`. Once currently passes no health-check
flags: `deployArgs` (internal/docker/proxy.go:221-233) builds

    kamal-proxy deploy <app> --target <id12> --deploy-timeout 120s [--host …] [--tls]

so kamal-proxy's built-in default governs the probe. If the readiness probe
fails, `Namespace.Deploy` destroys the app **and its volume**
(namespace.go:248-253).

Answer specifically, against the pinned image `basecamp/kamal-proxy:once-01`
(proxy.go:21) - not just upstream kamal-proxy docs, since the pin may lag:

1. What flags does `kamal-proxy deploy` accept for health checking
   (`--health-check-path`, `--health-check-interval`, `--health-check-timeout`,
   or equivalents)? Give exact flag names and value formats.
2. What is the default health-check path, and what does kamal-proxy consider a
   pass (status codes, redirects)?
3. Does the pinned `once-01` image support those flags? Verify by running
   `kamal-proxy deploy --help` in that image, not by reading upstream docs.
4. Is the health check re-run on an already-deployed target, or only at deploy
   time? Does changing the path later require a redeploy?
5. Does kamal-proxy support a target with an explicit port (`<id>:<port>`)?
   Once passes a bare container ID today - record what the port default is and
   whether it is overridable, since this constrains any future non-port-80 app.

Also confirm the second gate independently: Once's own `verifyHTTP` uses the
package const `HealthCheckPath = "/up"` (application.go:46). List every call
site that would need to read a per-app value instead.

## Resolution

**Yes.** `kamal-proxy deploy` in the pinned `basecamp/kamal-proxy:once-01` image
accepts `--health-check-path`, and it works against a real Vaultwarden container.
Verified empirically, not from docs.

### How it was verified

Image identity: `sha256:001c4f21bea1`, created `2026-03-12T16:18:09Z`, Go 1.26.1.
`go version -m` on the extracted binary reports
`github.com/basecamp/kamal-proxy (devel)` with **no `vcs.revision` stamp**, so the
exact upstream commit behind `once-01` is not recoverable; it is a custom build,
and there is no `once*` tag or branch in `github.com/basecamp/kamal-proxy`. Every
behavioral claim below was therefore re-checked against the running image; upstream
source (cloned at `c728fb5e`, 2026-07-16) is cited only for defaults and mechanism.
The image's `deploy --help` is a strict subset of upstream main (it lacks
`--tls-on-demand-url`, `--exclude-metrics-path`, `--client-ip-header`), confirming
the pin lags upstream, but every health-check flag below is present in the pin.

Test rig: `basecamp/kamal-proxy:once-01` + `vaultwarden/server:latest` +
`nginx:1.27-alpine` on a scratch Docker network; `docker exec` into the proxy to
drive the CLI. Confirmed Vaultwarden's own responses: `/alive` -> 200, `/up` -> 404.

### 1. Health-check flags in `once-01` (verbatim from `kamal-proxy deploy --help`)

```
--health-check-host string          Host header to send with health check requests
--health-check-interval duration    Interval between health checks (default 1s)
--health-check-path string          Path to check for health (default "/up")
--health-check-port int             Port to check for health (default matches target port)
--health-check-timeout duration     Time each health check must complete in (default 5s)
--deploy-timeout duration           Maximum time to wait for the new target to become healthy (default 30s)
--force                             Skip health checks and force deployment
```

Value formats: `--health-check-path` is a plain string joined onto the target URL
(leading `/` included, e.g. `/alive`); `--health-check-interval`, `-timeout` and
`--deploy-timeout` are Go durations (`1s`, `500ms`, `2m`); `--health-check-port`
is a bare int; `--health-check-host` is a raw Host header value.

Note `--force` exists and skips health checks entirely - a blunt escape hatch, but
it also skips the "did the app actually come up" signal, so it is not a substitute
for a correct path.

### 2. Default path and pass criteria

Default path `/up` (upstream `internal/server/service.go:39`,
`DefaultHealthCheckPath = "/up"`; also `DefaultHealthCheckPort = 0`,
`DefaultHealthCheckInterval = 1s`, `DefaultHealthCheckTimeout = 5s`, and
`DefaultDeployTimeout = 30s` at `service.go:34` - Once overrides that last one to
`120s`).

Pass = **final** status in `[200, 299]`. Upstream
`internal/server/health_check.go` (`func (hc *HealthCheck) check`) issues a plain
`GET` with `User-Agent: kamal-proxy` via `http.DefaultClient`, and fails on
`resp.StatusCode < 200 || resp.StatusCode > 299`.

Redirects are **followed**, not treated as a pass or a fail in themselves, because
`http.DefaultClient` applies Go's default policy (up to 10 hops). Confirmed
empirically in the pinned image: pointing the check at an nginx path that 301s to a
directory listing produced `Healthcheck failed ... unexpected status (403)` - the
*post-redirect* status. After adding an `index.html` so the same 301 landed on a
200, the identical deploy succeeded. So a 302 to a login page that returns 200 will
*pass*, which is a mild footgun when picking a path.

Failure log line (from the pin, useful for debugging):
`{"level":"INFO","msg":"Healthcheck failed","url":"http://kp-vw/up","error":"unexpected status (404)"}`.

### 3. Does `once-01` support them? (empirical)

Yes. Against `vaultwarden/server:latest`:

- `kamal-proxy deploy vw --target kp-vw --deploy-timeout 8s`
  -> `Error: target failed to become healthy within configured timeout (8s)`, exit 1
  (default `/up` -> 404). **This is exactly the Vaultwarden failure the ticket predicted.**
- `kamal-proxy deploy vw --target kp-vw --health-check-path /alive --deploy-timeout 15s`
  -> exit 0, `kamal-proxy list` shows `vw  *  /  kp-vw  running`.

So the single flag `--health-check-path /alive` is sufficient to unblock
Vaultwarden at the proxy gate.

### 4. When is the check re-run?

At deploy time, and then it **stops** for a single-target service once healthy.

- Mechanism: `LoadBalancer.updateHealthyTargets` (upstream
  `internal/server/load_balancer.go:262-266`) calls `lb.all.StopHealthChecks()`
  with the comment "If we have a single target, we can stop health-checking once
  it's healthy. Even if it becomes unhealthy later, taking it out of the pool
  won't help." Once always deploys one target, so this is always the Once case.
- Confirmed in the pin: after a healthy deploy, `docker stop` on the app container
  produced **no** further `Healthcheck failed` lines, `kamal-proxy list` still
  reported `running`, and requests through the proxy returned `502 Bad Gateway`.
  Restarting the container restored 200s with no state change. So kamal-proxy's
  service state is not a liveness signal for Once - do not read `list` as "app is up".
- Proxy restart does not re-check either: config is persisted per service in
  `/home/kamal-proxy/.config/kamal-proxy/kamal-proxy.state` (observed:
  `"health_check_config":{"path":"/alive","port":0,"interval":1000000000,"timeout":5000000000,"host":""}`)
  and restored targets are marked healthy outright via `MarkAllHealthy()`
  (upstream `internal/server/service.go:354`), which was visible in the pin's logs
  as `Restored saved state` with no subsequent health-check activity.
- **Changing the path later requires a redeploy.** There is no `kamal-proxy` verb
  to mutate an existing service's target options; `deploy` is the only writer.
- A failed redeploy is safely rolled back: redeploying an already-running service
  with a bad path failed with the timeout error, and the previously deployed target
  kept serving 200s with `"path":"/alive"` still in the state file. So probing a new
  path against a live service does not take it down. (Once still destroys the app
  container itself on failure - `namespace.go:248-252` - but that is Once's own
  policy, not kamal-proxy's.)

### 5. Target ports

`--target` accepts `<host>` or `<host>:<port>`. Upstream `parseTargetURL`
(`internal/server/target.go:473-480`) validates against
`^(\w[-_.\w+]+)(:\d+)?$` and parses `"http://" + target`, so a bare host means
**port 80** (Go's default for the `http` scheme). A 12-char container ID matches
`\w`-based pattern fine, which is why Once's bare-ID target works today.

Confirmed in the pin, with an app listening only on 8080:
- `--target kp-vw8080` (bare) -> failed;
  log: `dial tcp 172.18.0.4:80: connect: connection refused`. Port 80 default proven.
- `--target kp-vw8080:8080 --host app8080.test --health-check-path /alive` -> exit 0;
  `list` shows target `kp-vw8080:8080`. Explicit port proven.
- `--target kp-vw8080 --host hcport.test --health-check-path /alive --health-check-port 8080`
  -> exit 0; the health check hits 8080 while proxied traffic still goes to port 80
  (i.e. `--health-check-port` overrides the check only - upstream
  `Target.buildHealthCheckURL`, `internal/server/target.go:279-291`).

Practical note for a future non-port-80 app: `--target <id>:<port>` is the right
knob; `--health-check-port` is only for apps whose health endpoint lives on a
sidecar/admin port.

Gotcha found along the way: two services both claiming the wildcard host fail with
`Error: host settings conflict with another service` **even though their targets
already went healthy**. Deploy failures are therefore not always health-check
failures, and Once's error surfacing should not assume they are.

### Second gate: Once's own `verifyHTTP`

`docker.HealthCheckPath = "/up"` is declared at
`/home/arau/code/once/internal/docker/application.go:46`. Call sites that would
need a per-app value:

1. `/home/arau/code/once/internal/docker/application.go:497` - `verifyHTTP` builds
   `url + HealthCheckPath`; non-2xx -> `ErrVerificationFailed`. Reached from
   `VerifyHTTPOrRemove` (`application.go:255`), whose failure path removes the app
   (`Remove(ctx, true)` -> deletes the data volume), called by `Namespace.Deploy`
   (`/home/arau/code/once/internal/docker/namespace.go:258`). **This is the second
   kill switch, and it is independent of kamal-proxy's** - fixing only the proxy
   flag still leaves Vaultwarden destroyed here.
2. `/home/arau/code/once/internal/userstats/scraper.go:28` -
   `healthCheckPath = docker.HealthCheckPath`, used at `scraper.go:68` to drop
   health-check hits from user stats. It matches the proxy access log's `Path`
   field, and log lines carry a `Service` name, so a per-app path needs a
   service -> path lookup here, not a single const. Left as-is, Vaultwarden's
   `/alive` probes would be counted as real user traffic.
3. `/home/arau/code/once/internal/docker/application_test.go:27` asserts the
   requested path equals `HealthCheckPath` (plus `verifyHTTP` cases at lines 25,
   44, 58, 69, 79) - update alongside.

Not a call site, but adjacent and easy to confuse: `internal/background/admin.go:68`
serves `/up` for Once's *own* admin HTTP server. It is a literal, unrelated to app
health, and should not be parameterised.

`AppVolumeMountTargets` (`application.go:52`, `[]string{"/storage", "/rails/storage"}`)
call sites, for completeness:

- `/home/arau/code/once/internal/docker/application.go:538` - `volumeMounts`
  mounts the app volume at **every** entry.
- `/home/arau/code/once/internal/docker/application_backup.go:215` and `:221` -
  backup reads only `AppVolumeMountTargets[0]` (`/storage`) and re-prefixes the tar
  entries as `data/`.
- Asymmetry worth flagging: restore does **not** use the const at all -
  `populateVolume` (`application_backup.go:228-249`) mounts the volume at a fixed
  `/data` and untars into it. So changing `AppVolumeMountTargets[0]` silently
  changes what gets backed up but nothing about restore.
- No other hardcoded `/storage` or `/rails/storage` occurrences exist in the repo
  (grepped across all `*.go`).

Vaultwarden implication: Vaultwarden's data lives at `/data`, which is in neither
mount target, so it would get an empty volume mounted at `/storage` and
`/rails/storage` and back up nothing. That is outside this ticket's question but
gates the same feature - it needs its own ticket if one does not exist.

### Unanswered / caveats

- **Exact upstream commit for `once-01` could not be determined.** The binary has no
  VCS stamp (`(devel)`), and no `once*` ref exists upstream. Mitigated by verifying
  every behavior against the running image; upstream is cited for mechanism only.
- Behavior under **multi-target** services (continuous health checking, since
  `StopHealthChecks` is skipped when `multiTarget`) was read from upstream source,
  not exercised - Once only ever deploys a single target, so it does not apply today.
- `--health-check-host` was not exercised empirically (not needed for Vaultwarden).
