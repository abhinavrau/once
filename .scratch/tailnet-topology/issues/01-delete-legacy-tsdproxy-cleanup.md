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

**Status:** ready-for-agent

- [ ] No reference to `tsdproxy` remains in `internal/` or `integration/`
      except `removedTailscaleFields`
- [ ] `once tailscale <verb>` runs no extra container inspect before its body
- [ ] `once deploy --tailscale` fails as an unknown flag (cobra default), and
      `--backup-path` still fails with its clean-break message
- [ ] `Destroy` removes only the topology and the sidecar state volume
- [ ] `make build lint test` green; the integration tailscale tests compile
      and pass under `flock /tmp/once-integration.lock`
