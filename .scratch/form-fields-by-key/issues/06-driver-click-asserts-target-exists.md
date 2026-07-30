# 06 - Driver clicks assert the target exists

**What to build:** An integration driver click on a target no screen
registered fails the test immediately, naming the target - instead of being a
silent no-op that leaves the driver typing into whatever was focused.

Ticket 03's review proved the gap: rename a field key without updating its
driver and the test still passes, because the form's click handling falls
through on an unrecognized target. The AGENTS.md note currently papers over
this ("rename a key and its driver together"). Give the driver's click helper
a way to ask whether the target is currently registered - the mouse package
tracks registered click targets, but exposes no test-usable accessor - and
fail fast when it is not. Then shorten the AGENTS.md caveat accordingly.

**Blocked by:** None - can start immediately (ticket 03 is done).

**Status:** ready-for-agent

- [ ] A driver click on an unregistered target fails the test naming the
      target, demonstrated during development with a renamed key (not left in
      the suite)
- [ ] Clicks on registered targets behave exactly as before
- [ ] The accessor exposes no more than the existence check the driver needs
- [ ] The AGENTS.md rename-together caveat is updated to the new reality
- [ ] `make test` and `make lint` green; both UI integration tests pass
