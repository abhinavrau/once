# 02 - One progress-stream helper for Settings and InstallActivity

**What to build:** A deploy started from Settings can be cancelled at any
moment without wedging the docker goroutine, and both progress consumers
(Settings and InstallActivity) share one streaming helper instead of two
drifted copies.

Post-merge review found three related defects:

1. The Settings deploy progress callback is a bare blocking channel send, and
   the drain loop only re-arms while the section state is deploying. The
   section's cancel message has no state guard, so a cancel landing mid-deploy
   discards the model, leaves no drainer, and after the buffer fills the
   ApplySettings goroutine blocks forever - old container removed, new one
   never started. The send must not be able to block a deploy that has lost
   its listener (non-blocking send, or a done-channel escape), and cancel must
   be guarded or drained through.
2. Settings hand-rolls a second copy of InstallActivity's machinery (buffered
   channel + drain command + stage-to-percent mapping) with already-drifted
   details: different buffer sizes, different channel lifecycle, different
   context handling. Extract one shared helper in the UI package used by both,
   so the wedge fix and every future progress fix lands once.
3. Cosmetic, same flaw in both copies: the percent bar only renders the
   determinate bar for percent below 100, so the moment a pull completes the
   bar flips to the indeterminate braille animation until the next stage; a
   zero-percent report renders as blank spaces. Fix it in the shared helper.

**Blocked by:** None - can start immediately (ticket 01 is done).

**Status:** done

- [x] A cancel arriving while a Settings deploy is running neither wedges the
      deploy goroutine nor loses the deploy's completion, covered by a unit
      test that fills the progress buffer with no drainer armed
- [x] Settings and InstallActivity consume progress through one shared helper;
      the duplicated channel/drain/percent code is deleted
- [x] A 100-percent report renders as a full determinate bar, not the
      indeterminate animation; a 0-percent report renders as an empty bar
- [x] `make test` and `make lint` green; the TUI integration drivers still pass
      compile and vet

## Comments

**2026-07-30** - branch `progress-adapter-02-shared-progress-stream`

New `progressStream` in `internal/ui/progress_stream.go` is the one helper
both consumers use. A report is a non-blocking send that drops when the
16-slot buffer is full, so a task that outlives its listener can no longer
wedge: the old blocking send stranded `ApplySettings` with the old container
removed and the new one never started. Completion comes back as the `Run`
command's result, which Bubble Tea delivers whatever the model is doing, so
it cannot be dropped; `Wait` drains buffered reports under the finished case
so the ordering is total and no final frame is lost. `updates` is never
closed and `finished` is closed exactly once, so there is no send-on-closed
hazard.

`SettingsSectionCancelMsg` is now guarded to an idle form - the ticket's
"guarded or drained through". Guarding, not cancelling, is deliberate:
aborting a half-applied deploy would produce the very state the wedge
produced. Settings keeps its own screen until the run ends.

InstallActivity lost its drifted copy (buffer 10, `doneChan`, its own
ctx/cancel) along with `installProgressMsg`; it now maps
`docker.DeployProgress` onto its status text via `installStageFor` and
shares `progressBarPercent`. `Settings.running()` replaces the thrice-copied
run-state test.

`Progress.View` drew the determinate bar only below 100 percent, so a
finished pull flipped back to the braille animation; the unfilled cell was a
blank space, so a 0-percent report was invisible. Now any percent >= 0 draws
the bar, and the track renders as `⣀`. Two U+2014 em dashes in that file's
comments went with it.

Scope note: ADR-0012 forbids a *cross-frontend* progress module; this helper
is private to `internal/ui` and shared by two TUI screens only. The CLI
(`internal/command/cli_progress.go`), the Tailscale form, and the docker-side
reporter are untouched, and nil stays a legitimate headless callback.

`make test` (plus `-race -count=5` on `internal/ui`) and `golangci-lint`
green; `go vet ./integration/...` clean. Integration suite not run per
instructions. Reviewed with `code-review` against main; findings fixed in
9e93aea.

Not fixed, out of scope: `ctrl+c` in `internal/ui/app.go` still quits the
process mid-deploy, which can leave an app half-applied. Same failure class
as this ticket, different trigger - worth its own ticket.

- 2026-07-30 (coordinator): merged into arch-review-wave-3-tickets at dbf57c2. go vet, make test, make lint, and the full integration suite (498s) green on the merged tree. Done.
