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

**Status:** ready-for-agent

- [ ] Enable against a tailnet without Serve enabled fails within seconds (not the 90s advertise timeout) with an error naming the condition, the CLI-reported enablement URL, and the instruction to re-run `once tailscale enable` after enabling
- [ ] A tailnet with Serve already enabled sees no behavior change and no perceptible extra latency
- [ ] Detection runs on the SaaS path in both sidecar and host modes; the headscale control seam (no Services, verify skipped) is unaffected
- [ ] The probe never leaves a stray serve config or hung process behind
- [ ] Covered by unit tests at the daemon/exec seam with canned CLI output (SaaS paths cannot run in CI)
