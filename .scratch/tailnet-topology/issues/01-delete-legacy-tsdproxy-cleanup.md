# 01 - Delete the legacy TSDProxy cleanup path

**What to build:** Remove the code that reminds users about, and sweeps up,
the pre-ADR-0002 `once-tsdproxy` container. ADR-0002 declared that swap a
clean break with no users at the time; ADR-0015 reintroduces TSDProxy as the
Nodes provider, whose natural container name is `once-tsdproxy`, so the
reminder would fire on the live provider.

Delete: `Tailscale.LegacyTSDProxyWarning`, `legacyTSDProxyName`,
`legacyTSDProxyVolumeName`, the two extra removals in `Tailscale.Destroy`,
`withLegacyTSDProxyWarning` in `internal/command/tailscale.go` (the five
subcommands call their RunE directly), and the hidden removed `--tailscale`
flag plus its `checkRemoved` branch in `internal/command/settings_flags.go`
(the `--backup-path` removed flag stays). Keep `removedTailscaleFields`
(`apiKey`): that is the Label Codec's removed-field denylist (ADR-0008), not
cleanup code.

Drop the two integration asserts in `integration/tailscale_test.go` (~601,
~607) that exercise the warning, and any unit test that only exists for the
deleted code.

**Blocked by:** None - can start immediately.

**Status:** done

- [x] No reference to `tsdproxy` remains in `internal/` or `integration/`
      except `removedTailscaleFields`
- [x] `once tailscale <verb>` runs no extra container inspect before its body
- [x] `once deploy --tailscale` fails as an unknown flag (cobra default), and
      `--backup-path` still fails with its clean-break message
- [x] `Destroy` removes only the topology and the sidecar state volume
- [x] `make build lint test` green; the integration tailscale tests compile
      and pass under `flock /tmp/once-integration.lock`

## Comments

Deleted `Tailscale.LegacyTSDProxyWarning`, `legacyTSDProxyName`,
`legacyTSDProxyVolumeName`, the two extra removals in `Tailscale.Destroy`, and
the `withLegacyTSDProxyWarning` wrapper - the five tailscale subcommands now
use `WithNamespace` directly, so no subcommand inspects a container before its
body. Dropped the hidden `--tailscale` flag and its `checkRemoved` branch;
`--backup-path` keeps its clean-break message. `removedTailscaleFields`
(ADR-0008 denylist) and its test stay, and are the only remaining `tsdproxy`
mentions under `internal/`; `integration/` has none.

Tests: `TestRemovedTailscaleFlagFailsWithReplacement` became
`TestTailscaleFlagIsUnknown` (asserts cobra's `unknown flag: --tailscale`);
`TestCLIRemovedTailscaleFlagNamesReplacement` became `TestCLIRemovedFlags`
(unknown flag for `--tailscale`, clean-break message for `--backup-path`); the
teardown integration test no longer plants or asserts legacy remnants. Also
gofmt'd three files that had stray trailing blank lines.

`make build`, `make lint` (0 issues), `make test` all green. Full
`go test -count=1 -timeout 30m ./integration/...` under
`flock /tmp/once-integration.lock`: one failure, `TestBackupHookBehavior`, on a
ghcr.io token-fetch timeout (the known registry flake, unrelated to this
change); `docker pull` confirmed the route had recovered and the rerun of
`TestBackupHookBehavior|Tailscale` was `ok github.com/basecamp/once/integration
154.842s`.
