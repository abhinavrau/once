# 04 - Apply Settings spine on Namespace

**What to build:** Changing an Application's Configuration from the CLI and from
the TUI settings forms should run the same sequence. Today the CLI runs five
steps and the TUI runs two of them, which is why the TUI silently skips namespace
setup and the warn-only checks.

Give Namespace an Apply Settings entry point, named for the glossary term added
for this workflow. It owns: validate the new Configuration, set up the namespace,
reject a Host already taken by another Application, run the warn-only shadow and
DNS checks, swap the Configuration, roll the container, and restore the previous
Configuration if the roll fails.

Apply Settings is deliberately distinct from Update, which is image-driven, and
from Roll, which is only the container replacement. The CLI spells this workflow
`once update`, which is why the glossary now records the distinction.

The Configuration rollback that 01 adds to the TUI moves in here, so it exists
once rather than per frontend. Validation moves in too, and the CLI's remaining
hand-written validation call is deleted - after this ticket no frontend validates
Configuration itself.

Warnings return as a list beside the error, the same shape as the Deploy spine,
and each frontend renders them its own way.

**Blocked by:** 01 - TUI surfaces settings-deploy failures honestly.

**Status:** done

- [ ] Namespace exposes one Apply Settings entry point returning any warnings and an error
- [ ] The CLI update command and the TUI settings form submit both call it and contain no ordering of their own
- [ ] Invalid Configuration is rejected on both paths, and no frontend calls validation directly
- [ ] Changing Host to one already used by another Application is rejected on both paths
- [ ] A failed roll leaves the previous Configuration in place on both paths
- [ ] The TUI now performs namespace setup and emits the shadow and DNS warnings on a settings change
- [ ] A settings change that alters nothing still short-circuits without rolling the container
- [ ] Precondition ordering is unit-tested without a Docker daemon
- [ ] Integration tests pass, with CLI warning-ordering assertions updated

## Comments

2026-07-29 (coordinator): implemented and merged to main in commit 8875a95 (branch namespace-workflow-spine-tickets, merged before the candidate 02-07 review round). Status set to done to match.
