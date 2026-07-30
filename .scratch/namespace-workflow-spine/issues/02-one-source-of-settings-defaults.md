# 02 - One source of Application settings defaults

**What to build:** An Application installed through the TUI should start life
with the same Configuration defaults as one deployed from the CLI. Today it does
not: the CLI builds Configuration from its flags, each carrying a documented
default, while the TUI install path hand-builds a Configuration literal that sets
auto-update true and leaves every other field at its zero value. The two drift
apart silently whenever a new setting is added, because nothing connects them.

Introduce a single default Configuration that both paths start from, so adding a
setting means choosing its default once. The CLI's flag defaults and the TUI's
literal both derive from it.

This is a prefactor for 03: the deploy spine consumes Configuration built by both
frontends, and it is easier to move orchestration once both callers construct
Configuration the same way than to move it and then reconcile the defaults.

**Blocked by:** None - can start immediately.

**Status:** done

- [ ] One exported default Configuration exists and is the only place a field's default value is written
- [ ] The CLI's deploy flags take their defaults from it
- [ ] The TUI install path builds its Configuration from it rather than a hand-written literal
- [ ] An app installed via the TUI and one deployed via the CLI with no flags have identical Configuration apart from name, image, host and exposure
- [ ] A test fails if a Configuration field gains a non-zero default in one path but not the other

## Comments

2026-07-29 (coordinator): implemented and merged to main in commit d648ebf (branch namespace-workflow-spine-tickets, merged before the candidate 02-07 review round). Status set to done to match.
