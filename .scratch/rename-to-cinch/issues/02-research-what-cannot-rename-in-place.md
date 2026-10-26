# Research: which stored identity can be renamed in place?

Status: wontfix
Labels: wayfinder:research
Blocked by: (none)

## Question

For every place where a live install stores the name "once", what does it take
to change it, and what breaks if it isn't changed?

Cover at least:

- Docker containers: the name (`docker rename`), and labels (immutable, so a
  recreate; Once already rolls containers).
- Docker volumes: name and labels are immutable. Volume settings (such as
  encryption keys) live in the `once` label on the volume. What would moving
  the data to a new volume take?
- The systemd unit `once-background.service` and how the binary installs it.
- The state dir `~/.local/state/once` and any other host paths.
- kamal-proxy: do route or service names embed the namespace?
- Tailscale: device names, Tailscale Service names, the `tag:once` tag in the
  tailnet policy, and the TSDProxy / Docktail state volumes.
- restic backup repos: do repo paths, snapshot tags, or hostnames embed
  "once" or the namespace, and would a renamed install still find its
  snapshots?

Ground each answer in this codebase (file:line for where it is written and read)
and in Docker/Tailscale/restic docs for what can change in place.

## Comments

Closed as out of scope: the maintainer ruled that live installs are not migrated.
