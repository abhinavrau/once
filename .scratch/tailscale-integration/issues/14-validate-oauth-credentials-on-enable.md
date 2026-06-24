Status: needs-triage

# Validate Tailscale OAuth credentials before enabling

## Parent

- Reported during QA session
- PRD: `.scratch/tailscale-integration/PRD.md`

## What's wrong

When enabling Tailscale, Once accepts the OAuth client ID and secret and
proceeds straight to booting `once-tsdproxy` without ever checking that the
credentials actually work. If the credentials are wrong, expired, or lack the
required scopes, the enable flow still reports success — but tsdproxy never
registers a node on the tailnet, and nothing appears. The user is left with a
silently broken tailnet and no indication that the credentials were the problem.

## What I expected

When I enable Tailscale with OAuth credentials, Once should verify them against
the Tailscale API first and refuse to proceed if they're invalid, telling me
clearly that the credentials were rejected (e.g. bad client, missing scope).
The credentials should be validated before any container is booted, so a typo
fails fast instead of leaving a half-configured tailnet.

## Steps to reproduce

1. Enable Tailscale (via `once tailscale enable` or the TUI `t` settings form)
   with an OAuth client ID/secret that is wrong, revoked, or missing the scopes
   Once needs.
2. Observe the enable flow reports success and boots `once-tsdproxy`.
3. Check the tailnet — no node ever registers, and no error explains why.

## Additional context

The OAuth credentials are currently passed through to tsdproxy verbatim and are
only exercised asynchronously when tsdproxy tries to register. There is no
pre-flight check against the Tailscale API. Validating up front turns a silent,
delayed failure into an immediate, actionable error at the moment the user
submits their credentials. This mirrors the existing fail-fast precondition that
already guards the background daemon before enabling.

## Blocked by

None — can start immediately.
