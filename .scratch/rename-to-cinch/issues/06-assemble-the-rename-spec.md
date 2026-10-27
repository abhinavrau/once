# Assemble the rename spec

Status: done
Assignee: arau
Labels: wayfinder:grilling
Blocked by: 01, 04, 05

## Question

Write SPEC.md in this directory: the build-ready spec this map exists to
produce. It must contain:

- the old -> new table for every identifier (code, stored identity such as
  label key, namespace, container names and the Tailscale tag, env vars, paths,
  URLs, images, docs);
- the distribution changes from ticket 04;
- the text rules from ticket 05;
- an order of operations an agent can follow in one session, with a check
  that proves nothing named "once" is left that should have been renamed.

## Comments

Resolved: SPEC.md in this directory. Gaps found while assembling, settled in
the spec:
- launchd label prefix `com.basecamp.` -> `host.cinch.` (reverse of cinch.host).
- `once version` printed a bare version, so the installer's foreign-binary
  guard had nothing to match; `cinch version` prints `cinch <v> (cinch.host)`.
- Backup archive entries are renamed, so `once` backups don't restore into
  Cinch, consistent with no migration.
- The local dev environment item from Not yet specified became the last step
  of the order of operations.
