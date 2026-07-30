# 05 - Refocus the errored field on callback rejection

**What to build:** When a form's submit callback rejects the submit (exposure
form's tailnet-without-Tailscale or host-resolution failure, funnel form's
invalid duration), focus returns to the errored field - exactly as it already
does when a required field is empty. Today focus stays on the Save button, so
the user must tab back before typing, and error-clearing-on-input does not
fire until they do.

The required-field branch of the submit path already refocuses; the callback
branch records the errored field's key but leaves focus alone. Refocus by that
key in the callback branch too - one change in the shared form, fixing every
form that rejects from its callback, present and future.

**Blocked by:** None - can start immediately (tickets 02 and 04 are done).

**Status:** ready-for-agent

- [ ] A callback rejection moves focus to the errored field, unit-tested on
      the exposure form's tailnet-without-Tailscale path and the funnel
      form's invalid-duration path
- [ ] Typing in the refocused field clears the inline error, as it does after
      a required-field rejection
- [ ] Required-field rejection and successful-submit focus behavior unchanged
- [ ] `make test` and `make lint` green
