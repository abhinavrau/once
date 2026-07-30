# 01 - TUI shows pull percentage on settings apply and update check

**What to build:** When a TUI user changes an app's image in the Application
settings section and applies, or presses "Check for updates", the resulting
image pull currently animates an indeterminate progress bar for the whole
download - even though the CLI shows a live percentage for the identical
operations, and the TUI itself shows one on install. Wire real progress into
both paths so the settings screen's existing progress bar shows the download
percentage while pulling, and returns to indeterminate for the non-download
stages (starting, verifying).

Mechanism, mirroring the install activity's established pattern: the settings
model supplies a `docker.DeployProgressCallback` that feeds a channel drained
by a wait-for-progress command; a downloading stage sets the bar's percent,
any other stage sets it indeterminate. For the action path, the settings
action message's task changes from `func() (string, error)` to
`func(docker.DeployProgressCallback) (string, error)` - the same shape the
CLI's progress runner already uses. The funnel and backup actions ignore the
callback; the update action passes it through to the app update. The settings
apply path passes the callback instead of nil.

Do not build a shared progress module, extract a common channel pump, change
the CLI, or touch the background runner's nil - all rejected, see the PRD and
ADR draft.

**Blocked by:** None - can start immediately.

**Status:** done

- [x] Applying a settings change that carries a new image shows the download percentage on the settings progress bar, then goes indeterminate for starting/verifying
- [x] "Check for updates" shows the download percentage while a new image downloads
- [x] Actions that report no progress (funnel renew, back up now) behave exactly as before
- [x] No form fields are added, removed, or reordered, so the TUI integration form drivers are untouched
- [x] Unit tests cover the stage-to-bar mapping; `make test` and `make lint` pass

## Comments

**2026-07-29** - branch `progress-01-tui-pull-percentage`, worktree
`/home/arau/code/once/.claude/worktrees/agent-aff39ad39312afc59`

`Settings` now opens a buffered `chan docker.DeployProgress` per run, hands the
docker layer a callback that feeds it, and drains it with a wait-for-progress
command; `progressBarPercent` maps a downloading stage onto the bar's percent
and every other stage onto the indeterminate animation. The action carried by
`settingsRunActionMsg` takes a `DeployProgressCallback` - "Check for updates"
passes it to `Application.Update`, funnel and backup ignore it - and the apply
path passes it to `Namespace.ApplySettings` instead of nil. Apply and actions
share one `reporting` runner that closes the channel when the run ends, and a
run can only start from an idle form, so no docker goroutine can be left
blocked on an orphaned channel. No form fields changed; `make test` (plus
`-race -count=5` on `internal/ui`) and `golangci-lint` pass.

2026-07-29 (coordinator): merged into arch-review-frontier-tickets in 6b041dd; unit tests, lint, and the full integration suite green on the merged branch (437s, all pass). Status set to done.
