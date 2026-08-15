# 20 - Rewrite proxy settings without recreating the proxy

**What to build:** Changing proxy-held settings (the `backups` key, ports
untouched) no longer takes every app offline for the length of a container
recreate. Today the settings live in an immutable Docker label on the proxy
container, so every rewrite is force-remove-and-boot: a brief outage for
every routed app on each `backup enable`/`disable` or settings change.

This touches the "configuration lives in Docker labels" concept (AGENTS.md,
Data and State), so it starts with an ADR: where does mutable proxy-scoped
configuration live so a rewrite is an update, not a recreate? Candidates
include the proxy's volume (volume settings already ride volumes) - the
decision is the ticket's first deliverable, then the migration: old installs
with label-held settings must decode exactly as before, and a rewrite on an
old install moves the settings to the new home without an extra outage
beyond the one it was already paying.

Port changes may legitimately still recreate; the ticket only removes the
recreate where nothing about the container needs to change.

**Blocked by:** None - can start immediately. Not blocked by 19, and does
not replace it: a preloaded first boot still deserves 19's pull decision.

**Status:** ready-for-agent

- [ ] `backup enable`/`disable` on a running install leaves the proxy
      container id unchanged and drops no requests (integration test
      polling through the change)
- [ ] An install with label-held settings reads back unchanged; its first
      rewrite migrates them
- [ ] Port changes still apply (recreate allowed there)
- [ ] ADR recorded for the new home of mutable proxy configuration
- [ ] Lint, unit, and integration tests pass
