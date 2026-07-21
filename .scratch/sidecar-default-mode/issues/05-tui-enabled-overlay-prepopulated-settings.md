# 05 - TUI overlay shows stored settings on an enabled install

**What to build:** Ticket 03 left one criterion unsatisfiable: on an
already-enabled install the Tailscale overlay shows only a Disable button, and
the stored settings live on the once-docktail container whose existence is
what "enabled" means - so the mode selector and the stored mode never coexist.
Restructure the overlay so an enabled install shows the enable form's fields
pre-populated from the stored settings (client ID, secret, tag, and the mode
selector on the stored mode) instead of a bare Disable form. In this ticket
the fields are informational: Disable remains the form's submit, and
re-enabling from the TUI is ticket 06. Demoable as: enable Tailscale, reopen
the overlay, see exactly what it's running with.

Per AGENTS.md, TUI forms are integration-tested by driving fields in tab
order, and changing a form's fields silently breaks the driver (the form
rejects submit and the test hangs in `waitUntil` until timeout). The existing
UI integration test asserts the enabled overlay offers Disable; it must be
updated in the same change.

**Blocked by:** 03 - TUI enable form mode selector.

**Status:** ready-for-agent

- [ ] Opening the overlay on an enabled install shows client ID, secret (masked), and tag pre-populated from the stored settings
- [ ] The mode selector pre-populates with the stored mode (the criterion carried over from ticket 03)
- [ ] Disable remains the enabled overlay's submit and still works
- [ ] The form's integration driver and the enabled-overlay assertions are updated in the same change; the UI integration test passes without hanging
