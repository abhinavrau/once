# PRD - Address Form fields by key, not index

Architecture review candidate 06. Feature slug: `form-fields-by-key`.

## Problem

The TUI's Form module addresses fields positionally. Every form declares its own
`iota` block of field indices, field access goes through unchecked type
assertions by int, and the mouse-click targets are numbered by position. Tab
order therefore depends on the field count and on whether an action button was
added, and the per-form index constants silently drift when a field is added,
removed, or reordered.

Two consequences:

- The integration drivers encode tab counts. Inserting a field makes the
  driver's final tab land on the wrong field, enter advances focus instead of
  submitting, and the test hangs in `waitUntil` - burning 3 of the integration
  suite's 10 minutes before reporting a timeout instead of an assertion.
- A rejected submit is invisible from outside the form: `submitIfValid` just
  refocuses the errored field and renders the error inline, so a driver that
  waits for the overlay to close hangs the same way.

AGENTS.md carries a testing-notes hazard paragraph documenting exactly this
failure mode. Retiring that note is part of the win.

## Accepted design

The Form module in the ui package keeps its seam; only its interface changes.

1. **Fields are addressed by key.** `FormItem` gains a `Key` string; every item
   requires a unique non-empty key, enforced by panic at construction so drift
   dies in unit tests. Typed accessors take the key instead of an int and panic
   with the key and expected type on a miss. The per-form `iota` blocks are
   deleted; keys are plain string literals, used only within each form's own
   file. Mouse-click targets become `field:<key>`, joining the already-stable
   `submit`, `action`, and `cancel` targets. The one positional consumer, the
   environment form's dynamic key/value rows, generates its row keys from the
   row number. Along the way the dead `StaticField` type (no constructor call
   anywhere) and the `IsFocusable` machinery that exists only for it are
   deleted.

2. **Rejected submits emit a message.** `FormSubmitRejectedMsg{Key, Reason}` is
   emitted whenever a submit attempt does not go through: required-field
   validation failure, and an `OnSubmit` callback that sets a form error (the
   exposure form's path). The TUI keeps rendering the error inline; no
   production consumer is required - the message flows through the update
   chain harmlessly and exists for observers.

3. **Integration drivers stop counting tabs.** The driver focuses and operates
   fields by sending click events with `field:<key>` targets and submits via
   the `submit` target, so inserting or reordering a field no longer breaks
   any driver. The driver also fails the test immediately, with the field key
   and reason, when it sees `FormSubmitRejectedMsg` - converting the 3-minute
   timeout burn into a fast assertion. The AGENTS.md tab-order hazard note is
   replaced with the new driver guidance.

## Decisions (settled during grilling)

- Keys are string literals at call sites; no exported key constants. Each key
  is written twice, in the same file - locality over ceremony.
- Missing key or wrong field type panics with a descriptive message. It is a
  programmer error; error returns would infect every call site.
- No int-indexed access remains in the interface. `Focused()` stays an index
  because focus is inherently positional (the environment form's scroll math
  needs it).
- One uniqueness rule for all items, no exemptions.
- The rejection message alone would not retire the hazard: a mis-aimed tab
  lands on a field where enter advances focus and nothing rejects. Both the
  message and click-by-key addressing are needed.
- Radio fields cycle one option per click - existing production behavior,
  equivalent to what drivers do today with `space`; focus-then-arrow-keys
  covers precise selection. Per-option click targets are out of scope.
- No ADR: this is a ui-internal interface convention, not a load-bearing
  domain decision. The rewritten AGENTS.md note carries the operational
  knowledge. No new domain term emerged.

## Out of scope

- Per-option click targets on radio fields.
- Any change to CLI flows or to the forms' submitted message shapes.
- The progress-adapter concerns of candidate 07, even where the tailscale
  form touches them.

## Cross-feature dependencies

None. Possible textual merge adjacency with the namespace-workflow-spine
tickets in the integration UI test, but no ordering constraint in either
direction.

## Tickets

1. `01-key-addressed-form-fields` - no blockers
2. `02-form-submit-rejection-message` - blocked by 01
3. `03-drivers-click-by-key` - blocked by 01, 02
