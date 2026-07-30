# 03 - Quitting the TUI mid-action does not abandon a half-applied deploy

**What to build:** Pressing ctrl+c (or q, where quit is bound) while a
settings action or deploy is running no longer kills the process on the spot,
which can leave an app half-applied - old container removed, new one never
started. The TUI either finishes the in-flight action before exiting or makes
the user confirm they want to abandon it; an idle TUI quits instantly as
today.

Ticket 02 guarded the section-level cancel message for exactly this reason;
the app-level quit key is the same failure class through a different door.
Reuse the same notion of "an action is running" that the cancel guard uses,
so the two guards cannot drift apart.

**Blocked by:** None - can start immediately (ticket 02 is done).

**Status:** ready-for-agent

- [ ] Quit during a running settings action defers or requires confirmation;
      the in-flight action's completion is not lost, unit-tested
- [ ] Quit while idle exits immediately, as today
- [ ] Install-flow activity gets the same protection if it is exposed to the
      same quit path
- [ ] `make test` and `make lint` green
