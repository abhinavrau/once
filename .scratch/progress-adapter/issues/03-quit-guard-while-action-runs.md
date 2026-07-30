# 03 - Quitting the TUI mid-action does not abandon a half-applied deploy

**What to build:** Pressing ctrl+c (or q, where quit is bound) while a
settings action or deploy is running no longer kills the process on the spot,
which can leave an app half-applied - old container removed, new one never
started. The TUI either finishes the in-flight action before exiting or makes
the user confirm they want to abandon it; an idle TUI quits instantly as
today.

Ticket 02 guarded the section-level cancel message for exactly this reason;
the app-level quit key is the same failure class through a different door.
Reuse the same notion of "an action is running" that the cancel guard uses,
so the two guards cannot drift apart.

**Blocked by:** None - can start immediately (ticket 02 is done).

**Status:** done

- [x] Quit during a running settings action defers or requires confirmation;
      the in-flight action's completion is not lost, unit-tested
- [x] Quit while idle exits immediately, as today
- [x] Install-flow activity gets the same protection if it is exposed to the
      same quit path
- [x] `make test` and `make lint` green

## Comments

**2026-07-30** - branch `progress-adapter-03-quit-guard-while-action-runs`

Added a `Busy` interface to `internal/ui/component.go` (`Busy() bool`) that a
screen implements to mark itself as owning a task the process must not
abandon mid-flight. `App` now checks it through one private helper,
`screenIsBusy`, at both doors to shutdown+quit: the ctrl+c key press and
`QuitMsg` (the message dashboard's esc-bound quit and install's cliMode
cancel send through). A busy screen swallows the key/message (`return m,
nil`) before `shutdown` or `tea.Quit` ever run, so the docker goroutine
started by `stream.Run` keeps going exactly as if quit had never been
pressed - no cancelled context, no discarded model. An idle screen (or one
that doesn't implement `Busy` at all, like Dashboard or Logs) quits
immediately as before.

`Settings.Busy()` is the ticket's "reuse the same notion of an action is
running": it is the old private `running()` method, renamed and exported so
the app-level guard and the section's existing cancel guard (ticket 02) read
off the same state check and cannot drift. `Install.Busy()` reports true only
during `installStateActivity`, covering the install flow's exposure to the
same quit path.

Extra, in scope because it's the identical hazard: `Remove.Busy()` reports
`m.removing`. A removal killed mid-flight has the same failure shape as a
killed deploy - the container can be gone while the volume or proxy state
isn't updated yet - and cost one trivial method once `Busy` existed.

Unit-tested at the `App.Update` seam with fake `Busy` screens (idle exits,
busy guards, busy-then-idle exits) for both quit doors, plus integration
checks using the real `Settings` (mid-deploy, via `deployingSettings()`) and
`Install` (mid-activity) screens to confirm the guard fires and the run's own
completion message still lands normally afterward. `Settings.Busy()`,
`Install.Busy()`, and `Remove.Busy()` each got their own state-transition
test.

`make test`, `go test ./internal/ui/... -race -count=3`, `golangci-lint run`,
`go vet ./...`, and `gofmt -l` all clean. Reviewed with `code-review` against
main; the one real finding (`QuitMsg` wasn't guarded, only ctrl+c was) is
fixed via the shared `screenIsBusy` helper. Integration suite not run per
instructions.

- 2026-07-30 (coordinator): merged into arch-review-wave-4-tickets at bc67a16. go vet, make test, make lint, and the full integration suite (444s) green on the merged tree. Done.
