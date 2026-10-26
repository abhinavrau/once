# Assemble the rename spec

Status: open
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
