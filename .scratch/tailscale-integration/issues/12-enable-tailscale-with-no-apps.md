Status: done

# No way to enable Tailscale when no applications are deployed

## Parent

- Reported during QA session
- PRD: `.scratch/tailscale-integration/PRD.md`

## What's wrong

When there are no applications installed, Once opens the install wizard, and
there is no way to reach the global Tailscale settings form from there. The
`t` Tailscale binding only exists on the dashboard, but the dashboard is only
shown once at least one application exists. So a fresh install gives the user
no path to turn Tailscale on before deploying their first app.

## What I expected

I should be able to enable Tailscale on a fresh server — before deploying any
applications — so that the tailnet (and the Once admin web app) is ready, and
my first deployed app is exposed automatically.

## Steps to reproduce

1. Start Once on a server with no applications installed.
2. Observe you land in the install wizard ("there are no applications installed").
3. Look for a way to open the global Tailscale settings form (the `t` overlay).
4. There is none — Tailscale can only be configured from the dashboard, which
   requires at least one app.

## Additional context

The global Tailscale enable flow itself works with zero apps (the CLI
`once tailscale enable` succeeds and boots `once-tsdproxy` + `once-admin`); the
gap is purely that the TUI never surfaces the Tailscale form in the empty /
install state. A `t` binding (or equivalent entry point) on the empty-state /
install screen would close the gap.

## Blocked by

None — can start immediately.
