# 14 - `once tailscale status` rewrite

**What to build:** `once tailscale status` lists each exposed Application and the Once Admin Web App with its service URL (computed from the stored DomainSuffix) and its advertised/approved/pending state, using the local daemon for advertisement and the cloud API (when on Tailscale SaaS) for approval - reusing the sources built in ticket 13. Rejection/error surfacing comes from these explicit API/CLI states; the TSDProxy registration-error log scanning is no longer used by status.

**Blocked by:** 13 (verification sources).

**Status:** done

- [x] Status shows every exposed app and the admin app with URL and advertised/approved/pending state
- [x] Opted-out apps do not appear as exposed
- [x] A pending service shows its pending state (with the autoApprovers pointer), a rejected/unadvertised one shows an explicit error state
- [x] Rendering covered by unit tests driven through the daemon fake and httptest API
- [x] Lint and unit tests pass

## Comments

2026-07-13: Implemented as `Tailscale.ServiceStatuses` (internal/docker/tailscale_status.go),
reusing ticket 13's sources: one `ServeStatus` read through the ticket-10 daemon seam for
advertisement, and one `serviceApprovalState` cloud read per service (byte-compatible with
Docktail's listServiceHosts, asserted in the httptest tests) for approval. No polling -
status is a snapshot. States: `approved`, `pending approval (...autoApprovers.services rule
for <tag>)`, `not advertised (check the once-docktail container logs)`, and `advertised,
but not yet in the tailnet (...)` for a service the control plane doesn't know. On the
headscale control seam the cloud half is skipped and an advertised service just shows
`advertised`. The command lists once-admin first, then exposed running apps; stopped
exposed apps are listed last as `not running` rather than misreported as a Docktail error.
Opted-out apps were already filtered by `TailscaleExposed()`. No TUI form changed, so no
integration driver update. Lint and unit suites green.
