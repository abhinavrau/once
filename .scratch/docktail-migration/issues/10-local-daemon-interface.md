# 10 - Local tailscaled daemon interface

**What to build:** The single new seam from the PRD: a small interface through which Once reads everything it needs from a local tailscaled - the Magic DNS suffix, serve/services status (which services are advertised), and funnel status. Implemented with `tailscale ... --json` invocations, either by `docker exec` into the Once-managed sidecar or via the host CLI in host mode. Ships with a fake implementation driven by canned `status --json` / serve-status JSON so all later tickets can unit-test against it (Seam 2 in the PRD's Testing Decisions).

This is pure prefactoring: no user-visible behavior changes yet. "Make the change easy, then make the easy change."

**Blocked by:** None - can start immediately.

**Status:** done

- [x] An interface exists exposing: Magic DNS suffix, advertised services (serve status), and funnel activation state
- [x] A `docker exec` implementation (sidecar mode) and a host-CLI implementation (host mode) exist behind it
- [x] A fake implementation accepts canned JSON fixtures and is usable from unit tests
- [x] Unit tests cover parsing of suffix, advertised-services, and funnel-status JSON, including error/absent cases
- [x] Lint and unit tests pass; no existing behavior changes

## Comments

Implemented in `internal/docker/tailscale_daemon.go`:

- `TailscaleDaemon` interface with `MagicDNSSuffix(ctx)` (from `tailscale status --json`, preferring `.CurrentTailnet.MagicDNSSuffix` over the deprecated top-level alias, per issue 04) and `ServeStatus(ctx)` returning `TailscaleServeStatus{Services, AllowFunnel}` (from `tailscale serve status --json`; service names have the `svc:` prefix stripped and are sorted). Funnel activation state rides in `AllowFunnel` since `serve status --json` emits the full ServeConfig including it - no separate `funnel status` invocation needed.
- `NewHostTailscaleDaemon()` (host CLI via os/exec) and `NewSidecarTailscaleDaemon(client, containerName)` (docker exec, reusing the existing `execInContainer` helper) share one `cliTailscaleDaemon` implementation over a runner func, so parsing and exit-code handling are written once.
- `FakeTailscaleDaemon{StatusJSON, ServeJSON, StatusErr, ServeErr}` routes canned JSON through the same parse/strip helpers as the real path.
- Empty-serve-config nonzero exits are recognized by serve/funnel-specific phrases only (docktail's generic "not found"/"does not exist" matches were deliberately dropped: they would swallow real failures like a missing tailscaled socket as an empty status).
- Warning lines the CLI may print before the JSON (version mismatch) are stripped before parsing, mirroring docktail's stripWarnings.

Tests in `tailscale_daemon_test.go` cover suffix parsing (current field, deprecated alias, absent, bad JSON), serve-status parsing (advertised names, funnel map, empty config, bad JSON), CLI exit-code semantics (empty-config vs real failure vs runner error), and the host runner's process plumbing via a stub `tailscale` script. Wiring into enable/status/funnel flows is tickets 12-15.
