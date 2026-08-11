# 07 - Fail fast with the Serve enablement URL when the tailnet has Serve disabled

**What to build:** On a tailnet where Serve/Services has never been enabled,
`tailscale serve --service=...` prints an enablement URL
(`https://login.tailscale.com/f/serve?node=...`) and then blocks polling until
a human clicks it. Docktail execs that CLI, so its "Adding service" step hangs
forever, and docktail only logs command output on failure - the URL is
invisible everywhere. Today Once's enable times out after 90s with "not yet
advertised by the local tailscaled: ...; check the once-docktail container
logs", and those logs never show the link either. Found while e2e-testing the
crash-loop fixes: the only way to see the URL was to run the serve command by
hand inside the sidecar.

Instead, enable must detect that Serve is not enabled on the tailnet, fail
fast with an error that (1) says Serve/Services is not enabled on the tailnet,
(2) prints the actual enablement URL reported by the CLI (it embeds the node
ID, so it cannot be hardcoded), and (3) tells the user to click it and then
re-run `once tailscale enable ...`. A short-lived probe of the serve command
through the TailscaleDaemon seam (bounded timeout, killed after capturing
output) is the known-working detection; the daemon seam covers both sidecar
and host modes.

**Blocked by:** None - can start immediately.

**Status:** done

- [x] Enable against a tailnet without Serve enabled fails within seconds (not the 90s advertise timeout) with an error naming the condition, the CLI-reported enablement URL, and the instruction to re-run `once tailscale enable` after enabling
- [x] A tailnet with Serve already enabled sees no behavior change and no perceptible extra latency
- [x] Detection runs on the SaaS path in both sidecar and host modes; the headscale control seam (no Services, verify skipped) is unaffected
- [x] The probe never leaves a stray serve config or hung process behind
- [x] Covered by unit tests at the daemon/exec seam with canned CLI output (SaaS paths cannot run in CI)

## How it was built

`TailscaleDaemon` gained `ServeEnablementURL`, which runs
`tailscale serve --bg --https=443 once-serve-probe://invalid` and returns the
enablement link the CLI printed, or "" when Serve is already enabled.
`VerifyServices` calls it through `requireServeEnabled` before the 90s
advertise wait, so the enable fails in seconds with the link and the re-run
instruction.

The probe invocation was chosen against the upstream CLI source
(`cmd/tailscale/cli/serve_v2.go` `runServeCombined`,
`cmd/tailscale/cli/serve_legacy.go` `enableFeatureInteractive`):

- `--https` selects the HTTPS serve type the enablement check is gated on, and
  the check runs before any write to the serve config or to prefs.
- The target's scheme is rejected by `ipn.ExpandProxyTargetValue` immediately
  after that check, so on an enabled tailnet the command exits having written
  nothing - no stray serve config, no advertised service, no measurable latency.
- `--bg` (with no `--service`) skips both `addServiceToPrefs` and the
  foreground `WatchIPNBus` session, so nothing else is created on the way to
  the rejection.

Bounding differs by transport, which is why the daemon has a separate `probe`
runner: a host CLI is killed through its context (plus `WaitDelay`, so an
orphaned child holding the output pipes cannot stall the probe - a real hang
the tests caught), while a docker exec's process survives its attach being
dropped, so the sidecar wraps the command in busybox `timeout` inside the
container.

A probe that cannot run at all is treated as inconclusive and lets the existing
wait proceed, rather than blocking an enable on a diagnosis it cannot support.

Unit tests cover the daemon/exec seam with canned CLI output for both the
disabled and enabled tailnet, the probe's argument list and time bound, the
`timeout` wrapping, a killed host command keeping its partial output, and the
error `requireServeEnabled` produces.
