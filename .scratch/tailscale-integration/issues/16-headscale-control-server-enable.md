Status: needs-triage

# Let users enable Tailscale against a self-hosted Headscale control server

## Parent

- Reported during QA session
- PRD: `.scratch/tailscale-integration/PRD.md`

## What's wrong

Enabling Tailscale only works against Tailscale's hosted service via OAuth
credentials. Users who run their own Headscale control server have no supported
way to point Once at it. The capability technically exists — once-tsdproxy can
be configured with a control server URL and auth key instead of OAuth — but it's
only reachable through undocumented environment variables used by the test
harness, and the chosen control server is not persisted with the rest of the
Tailscale settings.

## What I expected

When enabling Tailscale, I should be able to opt to use my own Headscale control
server by providing its URL and an auth key, as an alternative to Tailscale SaaS
OAuth credentials. Once should use those instead of OAuth, and remember them so
the tailnet still works across restarts without re-supplying them through the
environment.

## Steps to reproduce

1. Run `once tailscale enable` (or open the TUI settings form).
2. Look for a way to provide a Headscale control server URL and auth key instead
   of Tailscale OAuth credentials.
3. There is none — only OAuth client ID/secret are offered, and the control-URL
   path is reachable only via undocumented environment variables.

## Additional context

This is the first-class promotion of the existing hidden control-server seam:
when a control URL + auth key are supplied they replace OAuth entirely (OAuth is
Tailscale-SaaS-only and can't reach a Headscale control plane). The two modes are
mutually exclusive. This issue covers the durable settings model (persisting the
control URL + auth key alongside the OAuth fields) and the CLI enable surface;
the TUI surface is a separate issue. Validation/credential-checking of the chosen
mode is tracked separately (#14).

## Blocked by

None — can start immediately.
