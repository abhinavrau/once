# 10 - Warn on unresolvable service URLs in `once tailscale status`

**What to build:** `once tailscale status` prints a row per service - once-admin
plus each tailnet-exposed app - with its URL and its advertised/approved state.
Ticket 09 added a local-resolution warning to `once list` and `once status`, but
not here, so this command can still show `approved` next to a URL that is dead
from the machine the user is reading it on. That is the most misleading place
for it: the state column reads as an authoritative "this works".

Each row whose URL does not resolve from this machine gains the same warn-only
note that ticket 09 introduced, with the same conditional wording - the service
may still be registering; or, if this machine is on the tailnet, its MagicDNS
may be broken (suggest toggling `tailscale set --accept-dns=false` then
`=true`); other tailnet devices may be unaffected. The check must never block or
fail the command.

The gating cannot be app-based the way ticket 09's is. once-admin has no
`Application` behind it, and a row's liveness is already expressed by its state
column rather than by a container's Running flag. So the shared warning helper
needs its app-shaped gate lifted out into its callers, leaving a URL-and-resolver
core that each caller decides when to invoke. Do that prefactor first, keeping
`once list` and `once status` behaviour identical; then add the new caller.

Rows in a state that means the service is not there to resolve - "not running",
and Tailscale not enabled - are skipped rather than warned about, since their
URL is expected to be dead and a warning would be noise.

**Blocked by:** None - can start immediately. (Ticket 09 is merged. Note that
ticket 07 also edits `internal/command/tailscale.go`; sequence after it lands to
avoid a conflict.)

**Status:** done

- [x] Each `once tailscale status` row whose service hostname does not resolve locally shows the ticket 09 warning next to it, including the once-admin row
- [x] Rows whose state already means the service is absent ("not running") are skipped, not warned about
- [x] The warning wording and timeout match ticket 09's exactly - one shared implementation, not a second copy
- [x] The app-shaped gate moves out of the shared helper into its callers, and `once list` / `once status` behaviour is unchanged
- [x] `--json` output is untouched: `tailscaleStatusReport` keeps its stable shape and the JSON path performs no lookup
- [x] Lookup failure never blocks or errors the command; a short timeout bounds the check
- [x] Unit tests cover resolve-ok, resolve-fail, and the skipped states via the existing injectable resolver seam
