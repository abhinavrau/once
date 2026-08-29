# 09 - ui.Form: submit-only, right-aligned button row

**What to build:** `ui.Form` (internal/ui/form.go) cannot express the layout
every prototype screen converged on: a single Continue button in the bottom-
right corner, no Cancel. The prototype resorted to string surgery on the
rendered view (`rightAlignButtons` in cmd/prototype-tailnet-policy/main.go)
and its focus cycle still contained the invisible Cancel stop.

**Blocked by:** None.

**Status:** done

- [x] A form can be built without a Cancel button; esc still cancels via OnCancel.
- [x] The button row can right-align; the focus cycle contains only real controls.
- [x] Existing forms are unchanged unless they opt in.
- [x] Form driver click targets keep working (integration/ui_test.go).

## Comments

Implemented as two opt-in Form methods: HideCancelButton() drops Cancel from
the view, click targets, and focus cycle, and makes the form itself treat esc
as cancel (routed through OnCancel); AlignButtonsRight() places the button row
flush with the form content's right edge (trailing button margin trimmed so the
primary button touches the edge, matching the prototype's rightAlignButtons
output). Existing forms untouched: esc is only intercepted when Cancel is
hidden, and all indices/targets are unchanged by default. Covered by new unit
tests in internal/ui/form_test.go; driver targets (field:<key>/submit/action/
cancel) unchanged.
