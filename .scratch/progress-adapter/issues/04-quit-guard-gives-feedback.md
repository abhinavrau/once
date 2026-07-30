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

**Status:** ready-for-agent

- [ ] A quit pressed while the current screen is busy produces visible
      feedback or a deferred exit, unit-tested at the `App.Update` seam
- [ ] Quit while idle exits immediately with no new output
- [ ] The guard still reads the single `Busy` state; no duplicated
      busy-tracking
- [ ] `make test` and `make lint` green
