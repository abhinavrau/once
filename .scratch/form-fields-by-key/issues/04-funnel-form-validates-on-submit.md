# 04 - Funnel form validates on submit

**What to build:** Saving the Funnel settings form with an invalid or empty
duration is rejected at the form, like every other settings form: inline error
on the duration field, focus returned to it, and the standard rejection
message emitted - instead of spinning the progress state and surfacing a
form-level error after the action fails.

The Funnel form is the one settings form whose submit callback returns the
run-action message unconditionally and whose duration field is not required.
Parse the duration at submit time; a value the apply step would reject never
leaves the form. This closes the inconsistency in the rejection contract
introduced when the exposure form was converted.

**Blocked by:** None - can start immediately (ticket 02, the rejection
message, is done).

**Status:** in-review

- [x] Submitting an invalid or empty duration keeps the form open with an
      inline error on the duration field and emits the rejection message with
      that field's key, unit-tested
- [x] A valid duration submits exactly as before
- [x] No form fields are added, removed, or reordered (integration drivers
      depend on tab order)
- [x] `make test` and `make lint` green

## Comments

**2026-07-30** - branch `form-fields-by-key-04-funnel-form-validates-on-submit`

The Funnel submit callback now parses the duration field and hands
`applyFunnel` a `time.Duration` instead of a string, so an unparsed value can
no longer reach the apply step. New helper `parseFunnelDuration` rejects empty,
unparseable, and out-of-bounds values (it calls `docker.ValidateFunnelDuration`,
the same check `Namespace.EnableFunnel` applies), and the callback calls
`f.SetError("duration", ...)` and returns nil - so the shared machinery from
ticket 02 emits `FormSubmitRejectedMsg{Key: "duration"}` automatically, exactly
as the exposure form does. `applyFunnel` lost its own parse; the spine still
validates independently.

Validation is gated on the enable checkbox: only an enable spends the duration,
so a disable still submits whatever the field holds (blocking a disable on an
empty duration would be a new bug). That is also why the field stays
non-`Required` - `Required` would fire on the disable path too. No fields were
added, removed, or reordered.

Unit tests: parse-helper bounds, form-level rejection for empty / unparseable /
over-24h (each asserting the inline error, the rejection key, and that the
reason matches the rendered error), valid duration still returning
`settingsRunActionMsg`, and disable-with-empty-duration still submitting.

**FOR THE COORDINATOR - two items to reconcile at merge:**

1. **Focus is not returned to the duration field**, and this ticket could not
   fix it. `Form.submitIfValid` (internal/ui/form.go) only refocuses on the
   required-field branch; when an `OnSubmit` callback sets the error, focus
   stays on the Save button. `Form` exposes no focus-by-key setter, so this is
   unfixable from a form file, and form.go was outside this ticket's scope
   guard. The gap is pre-existing and shared with the exposure form - worth one
   line in `submitIfValid` (refocus `f.errorKey` in the callback branch too),
   which would fix both forms at once. Side effect while it stands:
   `clearErrorOnInput` does not fire until the user tabs back to the field.
2. **No funnel form driver exists in `integration/ui_test.go`** - grep for
   "funnel" across `integration/` hits only `tailscale_test.go`, which drives
   the engine (`ns.Application(...).EnableFunnel`) and never opens the form. So
   nothing this validation would newly reject is typed by any driver, and no
   driver edit is needed. Recorded because the task asked for the check.

Review findings fixed: stale constructor doc claiming all submit checks live in
`Namespace.EnableFunnel`, and a pass-through `docker.ErrorMessage` wrapper on
errors this file constructs itself (now `err.Error()`).

`make test` green, `PATH="$HOME/go/bin:$PATH" make lint` clean. Integration
suite not run.
