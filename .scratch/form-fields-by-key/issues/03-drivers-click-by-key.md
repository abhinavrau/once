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

**Status:** ready-for-agent

- [ ] No integration driver navigates a form by counting tabs; fields are
      reached by click target key
- [ ] The driver fails fast with the field key and reason when any form
      rejects a submit, demonstrated by a deliberate rejection during
      development (not left in the suite)
- [ ] Both UI integration tests pass under the suite's 10-minute budget
- [ ] The AGENTS.md tab-order hazard note is replaced with the click-by-key
      driver guidance; the neighboring testing notes are untouched
