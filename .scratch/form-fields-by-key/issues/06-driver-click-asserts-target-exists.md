# 06 - Driver clicks assert the target exists

**What to build:** An integration driver click on a target no screen
registered fails the test immediately, naming the target - instead of being a
silent no-op that leaves the driver typing into whatever was focused.

Ticket 03's review proved the gap: rename a field key without updating its
driver and the test still passes, because the form's click handling falls
through on an unrecognized target. The AGENTS.md note currently papers over
this ("rename a key and its driver together"). Give the driver's click helper
a way to ask whether the target is currently registered - the mouse package
tracks registered click targets, but exposes no test-usable accessor - and
fail fast when it is not. Then shorten the AGENTS.md caveat accordingly.

**Blocked by:** None - can start immediately (ticket 03 is done).

**Status:** done

- [x] A driver click on an unregistered target fails the test naming the
      target, demonstrated during development with a renamed key (not left in
      the suite)
- [x] Clicks on registered targets behave exactly as before
- [x] The accessor exposes no more than the existence check the driver needs
- [x] The AGENTS.md rename-together caveat is updated to the new reality
- [x] `make test` and `make lint` green; both UI integration tests pass

## Comments

**2026-07-30** - branch `form-fields-by-key-06-driver-click-asserts-target`

Added `Tracker.Registered` (and the package-level `mouse.Registered` wrapping
the default tracker) as a plain existence check over the most recent Sweep's
zones - no more than the driver needs. The integration driver's `click`
helper now views the current screen first (so the tracker reflects it), then
`t.Fatalf`s naming the target if it isn't registered, before sending the
`MouseEvent`. Demonstrated the fail-fast path during development by
temporarily renaming the image form's field key in
`internal/ui/install_image_form.go` without updating the driver: the install
test failed in under 2s naming `field:image`, instead of burning the 3-minute
`waitUntil` timeout; the rename was reverted before committing (no trace in
the diff). Shortened the AGENTS.md testing-notes caveat to describe the
assert-before-send behavior instead of "rename a key and its driver
together". `make test`, `make lint`, and both UI integration tests
(`TestUIInstallAndManageApp`, `TestUITailscaleFormEnablesViaControlSeam`) are
green.

- 2026-07-30 (coordinator): merged into arch-review-wave-4-tickets at ee7d37c. go vet, make test, make lint, and the full integration suite (444s) green on the merged tree. Done.
