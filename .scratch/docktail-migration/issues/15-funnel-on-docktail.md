# 15 - Funnel on Docktail

**What to build:** `once tailscale funnel enable <app> --duration` keeps its exact current user-facing semantics on the new mechanism: bounded duration (default 10m, max 24h), background daemon required and responsible for automatic teardown at expiry, and activation verified before success is reported. The mechanism becomes `docktail.funnel.*` labels applied by the same atomic container recreation (Roll), and the activation check moves from the tsdproxy API to the local daemon's funnel status (ticket 10's interface). Funnel disable and the TUI funnel form keep working identically.

**Blocked by:** 12 (core swap).

**Status:** done

- [x] Funnel enable applies `docktail.funnel.*` labels via Roll; disable strips them
- [x] Duration validation, default, and max are unchanged; background daemon still tears down at expiry
- [x] Success is reported only after activation is confirmed via the local daemon's funnel status
- [x] Activation-wait and expiry paths covered by unit tests through the daemon fake; integration test covers label lifecycle
- [x] Funnel TUI form unchanged in fields (or its integration driver updated in the same change)
- [x] Lint, unit, and integration tests pass

## Comments

2026-07-14: Implemented. Most of the mechanism landed with ticket 12 (labels via
Roll in EnableFunnel/DisableFunnel, duration bounds, daemon-required check,
expiry teardown in the background runner); this ticket added the per-app
activation check. The daemon seam now parses funnel backends from `serve
status --json` (Web handlers behind AllowFunnel-enabled endpoints, new
`TailscaleServeStatus.FunnelBackends`), and `WaitForFunnelActive(ctx, app)`
polls until a funnel proxies to this app's `http://<container-IP>:80` backend -
another app's active Funnel no longer counts, closing the false positive when
docktail rejects a conflicting funnel endpoint. The backend byte format was
verified against docktail 1.5.0 source: default direct mode programs exactly
`http://<container-IP-on-specified-network>:<funnel.port>`, and docktail's own
reconcile relies on that string round-tripping through funnel status. CLI and
TUI callers pass the app; the TUI form fields are unchanged, so its driver
needed no update. Unit tests cover backend parsing and the activation wait
(success / wrong-app / no-funnel / erroring daemon) through the daemon fake;
the expiry path stays covered by the existing FunnelExpired and wakeInterval
unit tests (teardown execution itself is a thin DisableFunnel call needing
real Docker). The topology integration test now drives the funnel label
lifecycle: enable writes docktail.funnel.* plus the expiry in the once label
atomically, disable strips them while service labels stay. Lint and unit
suites green. Integration: every test in the suite passed, but split across
two runs - the host's documented Docker Hub IPv6 flakiness (see AGENTS.md
testing notes) flapped mid-run on four attempts, each time timing out an
unrelated image pull (confirmed environmental: plain `docker pull` failed the
same way outside the suite). All tailscale/funnel tests, including the new
lifecycle coverage, passed in 10.6s. Re-run `make integration` for a
single-pass record once the route stabilizes or dockerd is forced to IPv4.
