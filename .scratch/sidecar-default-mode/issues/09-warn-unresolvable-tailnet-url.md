# 09 - Warn when a tailnet service URL does not resolve locally

**What to build:** When once displays a tailnet app's service URL (the status
view, after the service has had time to register - not the deploy path), it
attempts to resolve the URL's hostname from the machine the CLI is running on.
If resolution fails, it shows a warn-only note next to the URL: the name does
not resolve from this machine; if this machine is on the tailnet its MagicDNS
may be broken (suggest toggling `tailscale set --accept-dns=false` then
`=true`); other tailnet devices may be unaffected. A user whose local
tailscaled resolver is wedged (observed in the field: profile switch left an
empty DNS config, every query SERVFAILed) learns why the printed URL is dead
instead of concluding the deploy failed.

The check must never block or fail any operation, and the wording must stay
conditional: in sidecar mode the host is not required to be a tailnet member,
so a failed lookup can be a healthy state. Do not add the check to deploy-time
warnings - Docktail registers the Tailscale Service on a periodic reconcile,
so the name legitimately does not resolve at deploy-print time.

Reuse the existing warn-only DNS preflight seam (injectable resolver) so the
check is unit-testable without a tailnet.

**Blocked by:** None - can start immediately.

**Status:** done

- [x] Status view resolves each tailnet-exposed app's service hostname from the local resolver and shows a warning next to the URL when the lookup fails
- [x] Warning text is conditional (machine may simply not be on the tailnet) and includes the accept-dns toggle hint
- [x] Lookup failure never blocks or errors the command; a short timeout bounds the check
- [x] Unit tests cover resolve-ok (no warning) and resolve-fail (warning) via the injectable resolver seam

## Implementation notes

- `docker.TailnetDNSWarning` lives beside `PublicDNSWarning` in
  `internal/docker/dns_preflight.go` and shares its `lookupHost` seam. It
  returns a string, never an error, and is bounded by a 2s timeout.
- Wired into the prose output of `once status` and `once list` through the new
  `printAppLines` helper in `internal/command/list.go`, which reuses the
  existing `printWarnings` renderer so the note reads `Warning: ...` like every
  other once warning. The lookups run in parallel, so a wedged resolver costs
  the command one timeout rather than one per app.
- The `--json` output of both commands is untouched (stable machine-readable
  shape, and no reason to spend a lookup on it).
- Stopped apps are skipped: their service is deregistered, so a failed lookup
  says nothing about the local resolver.
- The warning names three possible causes and commits to none: the service may
  still be registering, MagicDNS on this machine may be broken, or this machine
  may simply not be on the tailnet.

## Deliberately out of scope

- `once tailscale status` prints service URLs too, and its control-plane state
  can read "approved" next to a locally-dead URL. Covering it would mean
  gating on service state rather than on an app, so it is left for a follow-up
  ticket if the confusion shows up there in the field.
- The TUI dashboard's "Tailnet URL" row re-renders on a 5s tick; a synchronous
  probe does not belong there. It would need a cached, async check.
- The deploy path is untouched, per the ticket.
