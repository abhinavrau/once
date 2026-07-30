# 03 - Integration drivers address fields by key

**What to build:** The integration UI drivers stop encoding tab counts, and a
rejected submit fails the test immediately instead of burning the suite's
budget in a timeout. This retires the AGENTS.md testing-notes hazard about
form drivers breaking silently when a form's fields change.

Driver changes:

- A click helper sends the ui mouse event with a named target. The app
  forwards it to the current screen, screens forward to open overlays, and the
  form resolves it - the same path production clicks take after coordinate
  resolution. Drivers focus a text field with `field:<key>` then type, operate
  a checkbox or radio by clicking it (one click toggles, or cycles one option,
  same as space today; arrow keys after a focusing click cover the rest), and
  press buttons via the existing `submit`, `action`, and `cancel` targets. No
  tab-count navigation remains in any driver.
- The driver's message pump fails the test on the spot when it sees the form
  rejection message, reporting the field key and reason. What is today a
  3-minute `waitUntil` timeout with a view dump becomes an immediate assertion
  naming the cause.

Both UI integration tests are rewritten to this style. The lifecycle waits
(overlay close, dashboard state) stay as they are.

AGENTS.md: replace the testing-notes paragraph that warns "adding, removing,
or reordering a form field silently breaks those drivers ... hangs in
waitUntil until timeout" with short guidance for the new reality: drivers
address form fields by click target key, and a rejected submit fails fast with
the field and reason. Keep the surrounding notes (10-minute budget, goroutine
dump reading) intact.

Mind the suite budget while working here: one hung test consumes the whole
10 minutes, so verify locally that a deliberately rejected submit now fails in
seconds, not by timeout.

**Blocked by:** 01 - Key-addressed Form fields, 02 - Form submit rejection
message.

**Status:** in-review

- [x] No integration driver navigates a form by counting tabs; fields are
      reached by click target key
- [x] The driver fails fast with the field key and reason when any form
      rejects a submit, demonstrated by a deliberate rejection during
      development (not left in the suite)
- [x] Both UI integration tests pass under the suite's 10-minute budget
- [x] The AGENTS.md tab-order hazard note is replaced with the click-by-key
      driver guidance; the neighboring testing notes are untouched

## Comments

**2026-07-30** - branch `form-fields-by-key-03-drivers-click-by-key`

The worktree branched from a main that predated tickets 01 and 02, so it was
fast-forwarded to `b50add6` before any work; the diff carries only this
ticket's changes.

`appDriver` gained `click`, `clickField`, `fillField`, and `selectRadioOption`.
`click` sends a `ui.MouseEvent` with a named target straight at `App.Update`,
which enters the update chain exactly where a resolved production click does:
App hands it to the current screen, the screen to any open overlay, and the
form resolves `field:<key>`. `selectRadioOption` clicks a radio until the view
shows the wanted option selected, so the driver names the option instead of
counting cycles. Every `tab`/`shift+tab` is gone from `integration/`; the
remove flow's confirmation now uses its existing `confirm` target too. The
lifecycle waits are unchanged.

`processMsg` fails the test on `ui.FormSubmitRejectedMsg`, reporting the field
key and reason. It runs on the test goroutine, so `t.Fatalf` is safe, and
because it only assumes the message arrives (never when), the unordered
`tea.Batch` from ticket 02 is not a problem.

Deliberate rejection, run during development and reverted: dropping the image
form's `fillField` call fails in 1.6s with `form rejected the submit on field
"image": Image is required`, against the 3-minute `waitUntil` timeout it
replaces. Both UI tests pass in 24s together (10.5s + 13.3s).

Review finding worth recording: a click on an unrecognized target is a silent
no-op (`Form.handleClick` falls through), so a renamed field key does not
reject - the driver keeps typing into whatever was focused. The AGENTS.md note
says so and asks that a key and its driver be renamed together. Making `click`
assert the target exists would need a new exported accessor on
`internal/mouse`'s tracker that only tests would use; that is worth its own
ticket, not a scope stretch here.

`make test` green, `make lint` clean, `go test ./integration -run TestUI`
green. Full integration suite not run (coordinator owns it post-merge).
