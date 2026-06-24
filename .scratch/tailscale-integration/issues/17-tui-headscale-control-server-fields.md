Status: needs-triage

# TUI: offer Headscale control server URL + auth key in the Tailscale settings form

## Parent

- Reported during QA session
- PRD: `.scratch/tailscale-integration/PRD.md`

## What's wrong

The TUI Tailscale settings form (the `t` overlay) only offers OAuth client ID
and secret. A user who wants to connect their tailnet to a self-hosted Headscale
control server has no way to enter the control server URL and auth key from the
TUI — they can only configure OAuth against Tailscale SaaS.

## What I expected

The Tailscale settings form should let me choose between Tailscale SaaS (OAuth)
and a self-hosted Headscale control server, and when I pick Headscale, enter its
URL and an auth key. Saving should enable the tailnet against that control
server, and re-opening the form should pre-populate what I previously entered.

## Steps to reproduce

1. Open the global Tailscale settings form (the `t` overlay).
2. Look for fields to enter a Headscale control server URL and auth key.
3. There are none — only OAuth client ID and secret are shown.

## Additional context

This is the TUI surface for the Headscale control-server option; the settings
model, lifecycle, and CLI surface are covered by #16. The two modes (SaaS OAuth
vs. Headscale control server + auth key) are mutually exclusive, so the form
needs to make clear which one is in effect.

## Blocked by

- #16 — needs the persisted control-server settings model and enable lifecycle.
