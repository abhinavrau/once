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

**Status:** ready-for-agent

- [ ] Submitting an invalid or empty duration keeps the form open with an
      inline error on the duration field and emits the rejection message with
      that field's key, unit-tested
- [ ] A valid duration submits exactly as before
- [ ] No form fields are added, removed, or reordered (integration drivers
      depend on tab order)
- [ ] `make test` and `make lint` green
