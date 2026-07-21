# 06 - TUI re-enable: edit and resubmit settings on an enabled install

**What to build:** With ticket 05 showing the stored settings on an enabled
install, make them editable: the enabled overlay's form submits a re-enable
(the same lifecycle the CLI runs), giving the TUI parity with CLI re-enable.
Submitting unchanged settings is an idempotent no-op; changing the mode (or
credentials, or tag) rebuilds the topology, exactly as the docker layer
already does for the CLI. Disable moves to a secondary action on the same
overlay so both paths stay reachable. Demoable as: enable in sidecar mode,
reopen the overlay, switch the selector to host, submit, and the install now
drives the host tailscaled.

Per AGENTS.md, TUI forms are integration-tested by driving fields in tab
order, and changing a form's fields or submit silently breaks the driver (the
form rejects submit and the test hangs in `waitUntil` until timeout). The
driver must be updated in the same change.

**Blocked by:** 05 - TUI overlay shows stored settings on an enabled install.

**Status:** in-review

- [x] Submitting the enabled overlay's form with settings unchanged completes without rebuilding the topology (idempotent re-enable)
- [x] Submitting with a different mode selected rebuilds and the install runs in the new mode
- [x] The host-mode pre-flight failures from ticket 02 surface in the form when re-enabling into host mode
- [x] Disable is still reachable from the enabled overlay and still works
- [x] The form's integration driver is updated in the same change; the UI integration test passes without hanging
