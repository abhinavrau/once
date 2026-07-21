# 03 - TUI enable form mode selector

**What to build:** The TUI Tailscale enable form (client ID, secret, tag per
ADR-0006) gains a mode selector defaulting to sidecar, giving the TUI parity
with the CLI's `--mode` flag from ticket 01. Pre-populating from stored
settings follows the same stickiness rule: an already-enabled install shows
its stored mode.

Per AGENTS.md, TUI forms are integration-tested by driving fields in tab
order, and adding a field silently breaks the driver (the form rejects submit
and the test hangs in `waitUntil` until timeout). The form's integration
driver must be updated in the same change.

**Blocked by:** 01 - `--mode` flag with sidecar default and sticky re-enable.

**Status:** in-review

- [x] Enable form shows a mode selector with sidecar preselected on a fresh enable
- [ ] On an already-enabled install the selector pre-populates with the stored mode
- [x] Submitting with host selected enables in host mode; sidecar likewise
- [x] The form's integration driver is updated in the same change and the UI integration test passes without hanging

## Comments

Implemented: `Mode` radio (Sidecar/Host) appended to the enable form after Tag,
sidecar preselected. Submit maps the selection through `tailscaleModeChoices`
into `TailscaleSettings.Mode`; the host-mode pre-flight runs via
`ns.EnableTailscale` as with the CLI. Integration driver gained one tab
(Tag -> Mode -> Enable); `TestUITailscaleFormEnablesViaControlSeam` passes in
~10s.

Criterion 2 (pre-populate stored mode) is structurally unsatisfiable in the
current TUI: the enable form only builds when `Enabled()` is false, and the
stored settings live on the once-docktail container whose existence IS
`Enabled()` - so wherever the selector exists, no stored mode exists (and
`Disable` removes the label, so nothing survives to a re-enable either). An
already-enabled install shows the Disable form instead. Satisfying it would
mean restructuring the overlay to show enable fields on an enabled install -
out of this ticket's scope; left unchecked for a human decision.

Related parity note: the TUI always submits an explicit mode (the radio has no
"unset"), so the docker layer's empty-mode paths - sticky re-enable and the
host-tailscaled-present notice - never trigger from the TUI. Defensible: the
notice exists because the CLI default is invisible, while the radio makes the
choice visible; and sticky is moot per the paragraph above.
