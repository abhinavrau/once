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

**Status:** in-review

- [x] A callback rejection moves focus to the errored field, unit-tested on
      the exposure form's tailnet-without-Tailscale path and the funnel
      form's invalid-duration path
- [x] Typing in the refocused field clears the inline error, as it does after
      a required-field rejection
- [x] Required-field rejection and successful-submit focus behavior unchanged
- [x] `make test` and `make lint` green

## Comments

**2026-07-30** - branch `form-fields-by-key-05-refocus-errored-field`

`Form.submitIfValid` (internal/ui/form.go) now refocuses `f.errorKey`
whenever `f.HasError()` is true after validation and the optional `OnSubmit`
callback run, instead of only on the required-field branch. The required-field
and callback-rejection paths both set `errorKey` before this check, so one
`focusIndex` call now covers both, and a successful submit (no error set)
takes the unchanged early return. `focusCmd` joins `rejectionCmd()` and the
callback's own `cmd` in the same `tea.Batch`.

Unit tests: a generic two-field form asserting a callback rejection refocuses
the errored field (including a second-field case, to rule out an
accidentally-always-index-0 fix), and that typing after refocus clears the
inline error. Same clears-on-input assertion added to the exposure form's
tailnet-without-Tailscale test and the funnel form's invalid-duration test,
plus a `Focused()` check on each confirming the right field gets focus.

Ran `mattpocock-skills:code-review` against main with the ticket as spec:
both axes came back clean (no missing/partial requirements, no scope creep,
no standards violations). One minor duplication was flagged as a judgement
call across the three test files' clears-on-input assertions, but it matches
existing convention in the same files, so left as is.

`make test` green, `PATH="$HOME/go/bin:$PATH" make lint` clean. Integration
suite not run (out of scope for this ticket per task instructions).
