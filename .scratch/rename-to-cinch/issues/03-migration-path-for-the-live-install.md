# How does the live install move from once to cinch?

Status: wontfix
Labels: wayfinder:grilling
Blocked by: 02

## Question

Choose how the maintainer's live install (namespace `once`: campfire,
vaultwarden, the tailnet setup, backups) becomes a cinch install. The options
include:

- a one-off `cinch` command that adopts `once`-labelled resources and rewrites
  them;
- back up with the 3-2-1 backup and restore onto a fresh cinch install
  (dogfoods bare-metal restore);
- a documented manual procedure.

This also settles: downtime tolerance, what happens to the tailnet devices and
the `tag:once` policy entry, and whether old backup snapshots must stay
restorable.

## Comments

Closed as out of scope: the maintainer ruled that live installs are not migrated.
