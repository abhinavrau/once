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

**Status:** ready-for-agent

- [ ] A cancel arriving while a Settings deploy is running neither wedges the
      deploy goroutine nor loses the deploy's completion, covered by a unit
      test that fills the progress buffer with no drainer armed
- [ ] Settings and InstallActivity consume progress through one shared helper;
      the duplicated channel/drain/percent code is deleted
- [ ] A 100-percent report renders as a full determinate bar, not the
      indeterminate animation; a 0-percent report renders as an empty bar
- [ ] `make test` and `make lint` green; the TUI integration drivers still pass
      compile and vet
