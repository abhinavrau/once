Status: needs-triage

# Fetch the tailnet domain suffix from the Tailscale API on enable

## Parent

- Reported during QA session
- PRD: `.scratch/tailscale-integration/PRD.md`

## What's wrong

Once never learns the tailnet's own domain suffix (the MagicDNS base, e.g.
`tailnet-name.ts.net`) directly from Tailscale. Today the only way it sees a
tailnet URL is second-hand, by reading back what tsdproxy reports after a node
has already registered. Until something registers, Once has no idea what the
tailnet is called, so it can't show the user where their apps will live or
construct tailnet URLs ahead of time.

## What I expected

Right after the OAuth credentials are validated on enable, Once should ask the
Tailscale API for the tailnet's domain suffix and remember it. With the suffix
known up front, Once can show the user the tailnet they're connected to and
build each app's `*.ts.net` Magic DNS URL without waiting on tsdproxy's lookup.

## Steps to reproduce

1. Enable Tailscale with valid OAuth credentials.
2. Before any app has registered a node, look for the tailnet's domain suffix
   (the `*.ts.net` base) anywhere in Once.
3. There is none — Once only surfaces a tailnet URL once tsdproxy has already
   registered and reported it back.

## Additional context

The domain suffix is a property of the tailnet itself and is available from the
Tailscale API as soon as the OAuth credentials authenticate, independent of
whether any node has registered. Fetching and persisting it on enable removes
the dependency on tsdproxy's lookup for knowing the tailnet's name, and gives a
stable basis for displaying and constructing tailnet URLs. Where the suffix is
stored and surfaced (alongside the other Tailscale settings, in the TUI details,
etc.) is open for the implementer.

## Blocked by

- #14 — needs the validated, authenticated Tailscale API call introduced there.
