# 02 - Form submit rejection message

**What to build:** A rejected form submit becomes observable from outside the
form. Whenever a submit attempt does not go through, the form emits

```go
type FormSubmitRejectedMsg struct {
    Key    string // the errored field's key
    Reason string // the error text the form renders inline
}
```

as a command, alongside its existing behavior (refocus the errored field,
render the error inline). Both rejection paths are covered:

- required-field validation fails before the submit callback runs
- the submit callback itself sets a form error and returns without
  submitting (the exposure form does this for tailnet-without-Tailscale and
  for host resolution failures)

The second path works because the submit callback receives the form by
pointer: after it returns, the form checks whether an error was set and
batches the rejection message with whatever command the callback returned.

No production consumer is added. The TUI already shows the error inline; the
message flows through the app's update chain and is ignored everywhere. It
exists for observers - the integration driver picks it up in the next ticket.

**Blocked by:** 01 - Key-addressed Form fields (the message carries the field
key).

**Status:** done

- [x] Submitting with a required field empty emits the rejection message with
      that field's key and the rendered reason, covered by a unit test
- [x] A submit callback that sets a form error causes the rejection message to
      be emitted, covered by a unit test driving the exposure form's
      tailnet-without-Tailscale path
- [x] A successful submit emits no rejection message
- [x] Inline error rendering and refocus behavior are unchanged
- [x] Unit and integration tests pass

## Comments

**2026-07-30** - branch `form-fields-by-key-02-form-submit-rejection-message`

`FormSubmitRejectedMsg{Key, Reason}` added to `internal/ui/form.go`.
`submitIfValid` now funnels both rejection paths through one emission point:
`validate` clears the error up front, so an error still set after either the
required-field check or the `OnSubmit` callback means the submit did not go
through, and the rejection command is batched with whatever command that path
produced. Refocus, inline rendering, and `View()` are untouched, and no
production code consumes the message.

Both submit entry points (`handleEnter` and a click on the `submit` target)
already funnelled into `submitIfValid`, so both are covered. No callback in the
codebase sets a form error asynchronously, so there is no path that bypasses
the emission.

Unit tests: rejection on required-field failure (key, reason, inline error,
refocus), rejection on the click-submit path since that is what ticket 03's
driver will use, rejection when a callback sets an error, and no rejection on a
successful submit. The exposure form's tests now assert the rejection on both
its tailnet-without-Tailscale and host-resolution paths instead of asserting a
nil command.

Review notes: `tea.Batch` does not order its commands, so ticket 03's driver
must not depend on the rejection arriving before the callback's own command.

`make test` green, `make lint` clean. Integration suite not run (wave 3 owns
the drivers).

- 2026-07-30 (coordinator): merged into arch-review-wave-2-tickets at e884ede (fast-forward). go vet, make test, make lint, and the full integration suite (489s) green on the merged tree. Done.
