# 01 - Key-addressed Form fields

**What to build:** Form fields are addressed by a string key instead of a
positional index, so adding, removing, or reordering a field in any TUI form
changes nothing but that form's own declaration.

`FormItem` gains a `Key`. Every item must carry a unique non-empty key; the
form panics at construction if not, so a violation dies in the unit tests. The
typed accessors (text, checkbox, radio) and `SetError` take the key; a missing
key or wrong field type panics with a message naming the key and expected
type. Mouse-click targets for fields become `field:<key>`.

Every form updates in the same change: keys are plain string literals written
where the field is declared and read back in the submit closure, and each
form's private field-index `iota` block is deleted. The environment form's
dynamic key/value rows generate their keys from the row number; its scroll
math keeps using the positional `Focused()` accessor, which stays.

Two pieces of dead code go with the indices: the static field type has no
constructor call anywhere in the repo, so it and the focusability machinery
that exists only to skip it (the focusable-check interface method, the
skip-unfocusable focus walk, and the constructor's find-first-focusable loop)
are deleted.

Tab order and rendering are unchanged, so the integration drivers keep passing
untouched; this ticket is unit-scope only.

**Blocked by:** None - can start immediately.

**Status:** done

- [x] Every form item is declared with a key, and no per-form field-index
      constant block remains in the ui package
- [x] Field access, error placement, and field click targets all go by key;
      no int-indexed field accessor remains in the Form interface
- [x] Constructing a form with a duplicate or empty key panics, covered by a
      unit test
- [x] Accessing an unknown key or the wrong field type panics with a message
      naming the key, covered by a unit test
- [x] The environment form's dynamic rows work by generated keys, including
      its append-empty-row rebuild and its rendering of visible rows
- [x] The static field type and the focusability machinery are gone
- [x] Unit tests in the ui package are updated to keyed access and pass
- [x] Integration tests are untouched by this ticket and still pass

## Comments

2026-07-29 - branch `form-keys-01-key-addressed-fields`, worktree
`/home/arau/code/once/.claude/worktrees/agent-a9c929bd159128125`.

`FormItem` now carries a required unique `Key`; the typed accessors, `SetError`,
and the `field:<key>` click targets all address by key, and all ten per-form
index constant blocks are gone (the environment form generates `key-N`/`value-N`
from the row number and keeps its positional `Focused()` scroll math). The dead
`StaticField`, the `IsFocusable` interface method, the skip-unfocusable focus
walk, and the constructor's find-first-focusable loop are deleted. `make test`
and `golangci-lint` are clean, and the diff touches only `internal/ui`, so tab
order, rendered text, and submit behavior - and therefore the integration
drivers - are unchanged.

2026-07-29 (coordinator): merged into arch-review-frontier-tickets in 0512f40; unit tests, lint, and the full integration suite green on the merged branch (437s, all pass). Status set to done.
