Status: done

## Resolution

Added a durable per-app exposure choice (`ApplicationSettings.TailscaleExcluded`,
stored inverted so the default and pre-existing apps stay exposed). The tsdproxy
labels are now gated on `tailscaleEnabled && Settings.TailscaleExposed()`.

Controls:
- TUI deploy time: "Expose on the tailnet" checkbox on the install hostname
  screen (shown only when Tailscale is globally enabled, default checked).
- TUI afterward: new Settings → Tailscale section with the same checkbox.
- CLI both: `--tailscale` flag on `deploy`/update (default true), e.g.
  `once deploy <image> --tailscale=false`.

Deferred: a headscale integration scenario asserting a hidden app registers no
node (label-gating is covered by unit tests; the harness already proves the
exposed path).


# Tailscale exposure is global all-or-nothing — no per-app opt in/out

## Parent

- Reported during QA session
- PRD: `.scratch/tailscale-integration/PRD.md` (§1 lists per-app opt-out as out of scope for v1)

## What's wrong

Tailscale is a single global on/off switch. When it's enabled, every deployed
application is retrofitted with `tsdproxy.*` labels and exposed on the tailnet —
there is no way to deploy or keep an application off the tailnet while Tailscale
is enabled. Enabling Tailscale re-rolls all running apps to add the labels;
disabling re-rolls them all to strip the labels. Individual apps have no say.

## What I expected

I should be able to choose, per application, whether it is exposed on the
tailnet — both at deploy time and afterward — while Tailscale is globally
enabled. Some apps I want reachable over the tailnet; others I want to keep
private or only on the proxy. Turning Tailscale on globally should not force
every app onto the tailnet.

## Steps to reproduce

1. Enable Tailscale globally.
2. Deploy an application.
3. Observe the app is automatically exposed on the tailnet (gets a Magic DNS
   node) with no option to opt out.
4. Look for a per-app "expose on Tailscale" toggle at deploy time or in the
   app's settings — there is none.

## Additional context

Today the only per-app Tailscale control is Funnel (temporary public access via
`FunnelExpiresAt`), which is a duration, not an opt-out — an app with Funnel
off is still on the tailnet. This issue is about a durable per-app exposure
choice (default could remain "exposed" to preserve current behavior). This was
deliberately deferred in the v1 PRD; this issue reopens it based on QA feedback.

## Blocked by

None — can start immediately. (Independent of #12.)
