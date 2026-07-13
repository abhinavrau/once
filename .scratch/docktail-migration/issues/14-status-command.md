# 14 - `once tailscale status` rewrite

**What to build:** `once tailscale status` lists each exposed Application and the Once Admin Web App with its service URL (computed from the stored DomainSuffix) and its advertised/approved/pending state, using the local daemon for advertisement and the cloud API (when on Tailscale SaaS) for approval - reusing the sources built in ticket 13. Rejection/error surfacing comes from these explicit API/CLI states; the TSDProxy registration-error log scanning is no longer used by status.

**Blocked by:** 13 (verification sources).

**Status:** ready-for-agent

- [ ] Status shows every exposed app and the admin app with URL and advertised/approved/pending state
- [ ] Opted-out apps do not appear as exposed
- [ ] A pending service shows its pending state (with the autoApprovers pointer), a rejected/unadvertised one shows an explicit error state
- [ ] Rendering covered by unit tests driven through the daemon fake and httptest API
- [ ] Lint and unit tests pass
