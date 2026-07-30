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

**Status:** ready-for-agent

- [ ] Submitting with a required field empty emits the rejection message with
      that field's key and the rendered reason, covered by a unit test
- [ ] A submit callback that sets a form error causes the rejection message to
      be emitted, covered by a unit test driving the exposure form's
      tailnet-without-Tailscale path
- [ ] A successful submit emits no rejection message
- [ ] Inline error rendering and refocus behavior are unchanged
- [ ] Unit and integration tests pass
