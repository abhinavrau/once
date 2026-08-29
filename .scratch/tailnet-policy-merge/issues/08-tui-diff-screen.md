# 08 - TUI: tagOwners step and the Merged Policy diff screen

**What to build:** The production TUI screens the prototype validated, lifted
from `cmd/prototype-tailnet-policy/main.go` (branch
`prototype/tailnet-policy`) into `internal/ui/` beside `tailscale_form.go`
and the custody-flow pattern (`custody.go`).

Carry over what the prototype settled (ADR-0017 amendment):

- tagOwners screen: three numbered steps, both entry shapes labelled, one
  copy path shared by the visible button and ctrl+p, skip for existing tags.
- Diff screen: summary line, viewport auto-scrolled to the first addition,
  overview-ruler markers on the scrollbar, soft-wrapped added lines, copy
  control outside the scrolling region, exact-height chrome accounting
  (the prototype's diffChrome + TestDiffScreenFitsSmallTerminals pattern).
- Topology as the credential form's first field with a live scope list.
- Continue bottom-right, no Cancel button, `Copied ✓` state.

**Blocked by:** 06, 07, and 09 for the button layout.

**Status:** ready-for-agent

- [ ] Both screens fit and stay usable at 80x24; a size test pins the chrome like the prototype's.
- [ ] The copy control is a named click target the integration form drivers can assert (`integration/ui_test.go` conventions).
- [ ] The credential screen does not overflow the 24-row floor - coordinate with `.scratch/tailnet-topology/issues/07-enable-form-overflows-small-terminals.md` (scroll or collapse the guidance steps; the prototype's version measured 38 rows).
- [ ] VHS-style verification at 24/46/60 rows, or an equivalent height-pinned render test.
