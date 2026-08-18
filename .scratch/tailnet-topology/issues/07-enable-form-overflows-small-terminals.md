# 07 - The Tailscale enable form overflows terminals under ~46 rows

**Problem:** With the Topology radio (ticket 05) and, under Services, the
Mode radio, the TUI enable form renders at ~46 rows. `OverlayCenter` clips an
overlay taller than the window at both ends, so at the 80x24 minimum size the
form's title and its help line (`ctrl+p` policy snippet, `esc close`)
disappear; the fields and buttons stay usable. Before ticket 05 the form
already overflowed under ~38 rows; the Topology field widened the gap.
Found when `TestUITailscaleFormEnablesViaControlSeam` fell through at 120x40;
the test now runs at 120x50 and asserts the help line is visible.

**Candidate fixes:** compact radios (label and options on one row, saving
~3 rows per radio - the recommended option: it lowers the floor for every
form using `RadioField`, e.g. remove.go, exposure); a scrollable overlay
(more state, more UI surface); a shorter intro (fights the adapter-supplied
prose from ticket 05). Whichever lands, add a form-height assertion in the
ui unit tests at the guard's minimum, so a new field cannot silently push
past 24 rows again.

**Blocked by:** Nothing; separate follow-up.

**Status:** needs-triage
