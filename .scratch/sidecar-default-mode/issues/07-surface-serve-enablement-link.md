# 07 - Surface the "Serve is not enabled on your tailnet" enablement link

**What to build:** On a tailnet where Serve/Services has never been enabled,
`tailscale serve --service=...` prints an enablement URL
(`https://login.tailscale.com/f/serve?node=...`) and then blocks polling until
a human clicks it. Docktail execs that CLI, so its "Adding service" step hangs
forever, and docktail only logs command output on failure - the URL is
invisible everywhere. Once's enable then times out after 90s with "not yet
advertised by the local tailscaled: ...; check the once-docktail container
logs", but those logs never show the link either. Found while e2e-testing the
crash-loop fixes: the only way to see the URL was to run the serve command by
hand inside the sidecar.

Options (decide at implementation time):
- Enable-time pre-flight: after the sidecar logs in, run a short-lived
  `tailscale serve` probe through the TailscaleDaemon seam, detect the
  "Serve is not enabled on your tailnet" output, surface the URL, and fail
  fast (or wait, naming what is being waited on).
- Detect the condition during the advertise wait and rewrite the timeout
  error to include the enablement URL.

**Blocked by:** None.

**Status:** needs-triage

- [ ] An enable against a tailnet without Serve enabled surfaces the enablement URL instead of a generic advertise timeout
- [ ] A tailnet with Serve already enabled sees no behavior change and no extra latency
- [ ] Works on the SaaS path; the headscale control seam (no Services) is unaffected
