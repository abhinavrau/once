# 04 - A guarded quit tells the user why nothing happened

**What to build:** Pressing ctrl+c (or q, where quit is bound) while an
action is running currently does nothing visible: ticket 03's guard swallows
the key to protect the in-flight deploy, but gives no feedback, so the TUI
can read as hung to a user who expects quit to work. Show why the quit was
refused - for example a brief inline notice near the running action ("busy -
finishing current action; quit when done") - or defer the quit so the TUI
exits on its own once the action completes. Either satisfies the original
ticket's "defers or requires confirmation" wording, which ticket 03 shipped
below.

Hang the feedback off the same `Busy` check `screenIsBusy` uses in
`internal/ui/app.go`; do not add a second notion of "an action is running".
An idle quit stays instant and silent as today.

**Blocked by:** None - can start immediately (ticket 03 is done).

**Status:** done

- [x] A quit pressed while the current screen is busy produces visible
      feedback or a deferred exit, unit-tested at the `App.Update` seam
- [x] Quit while idle exits immediately with no new output
- [x] The guard still reads the single `Busy` state; no duplicated
      busy-tracking
- [x] `make test` and `make lint` green

## Comments

**2026-07-30** - branch `progress-adapter-04-quit-guard-gives-feedback`

Picked visible feedback over deferring the quit: `App` now shows a brief
notice ("Busy - finishing current action; quit when done.") overlaid on the
current screen instead of silently swallowing ctrl+c/`QuitMsg` while
`screenIsBusy()` is true. Both guarded doors in `internal/ui/app.go` now call
a new private `showQuitNotice()`, which sets a `quitNoticeShown` bool and
returns a `tea.Tick`-scheduled cmd that clears it after `quitNoticeDuration`
(2s) via a new `quitNoticeExpiredMsg`. `View()` composites the notice on top
of the current screen's rendered content with the existing `OverlayCenter`
helper (the same compositing `install.go` already uses for `PopupHelp`), so
the notice never changes the screen's own layout or height budget - no new
line is inserted, just an overlay box. The guard itself is untouched: both
doors still read the single `screenIsBusy()`/`Busy` check from ticket 03; no
second busy-tracking was added. Idle quit is unchanged (still an immediate
`m.shutdown(); return m, tea.Quit`, no new cmd, no notice).

Unit-tested at the `App.Update` seam: a new test confirms the guarded ctrl+c
path renders the notice text in `View().Content`, and another confirms the
notice clears once its `quitNoticeExpiredMsg` lands. The four existing
ticket-03 tests that asserted the guarded quit returns a nil cmd were updated
to assert the new non-nil (tick) cmd and, where relevant, the visible
`quitNoticeShown` flag, since ticket 04 deliberately changes that swallow to a
scheduled notice.

`make test`, `go test ./internal/ui/... -race -count=3`, `go vet ./...`,
`gofmt -l .`, and `PATH="$HOME/go/bin:$PATH" golangci-lint run` all clean.
Reviewed with `code-review` against main (spec source: this ticket file) -
both the Standards and Spec sub-agent reports came back with no hard
violations and no missing/wrong requirements; the one nit taken (grouping two
related top-level `const` declarations into one `const (...)` block) was
applied. Scope stayed to `internal/ui/app.go` and its test file; did not
touch `internal/ui/component.go`, `internal/docker/`, `integration/`, or
`AGENTS.md`. Integration suite not run per instructions.

- 2026-07-30 (coordinator): fast-forwarded to main at e0d80f0. go vet, make test, make lint green. Full integration suite: first run had one unidentified failure (output lost to a tail pipe; UI tests passed in that run), full rerun green 36/36 in 469s - consistent with the known registry-flake signature. Done.
