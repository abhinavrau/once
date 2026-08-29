# 09 - ui.Form: submit-only, right-aligned button row

**What to build:** `ui.Form` (internal/ui/form.go) cannot express the layout
every prototype screen converged on: a single Continue button in the bottom-
right corner, no Cancel. The prototype resorted to string surgery on the
rendered view (`rightAlignButtons` in cmd/prototype-tailnet-policy/main.go)
and its focus cycle still contained the invisible Cancel stop.

**Blocked by:** None.

**Status:** ready-for-agent

- [ ] A form can be built without a Cancel button; esc still cancels via OnCancel.
- [ ] The button row can right-align; the focus cycle contains only real controls.
- [ ] Existing forms are unchanged unless they opt in.
- [ ] Form driver click targets keep working (integration/ui_test.go).
