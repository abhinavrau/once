# 06 - TUI remove lets the user keep the data volume

**What to build:** Removing an Application from the TUI currently always destroys
its data volume: the confirmation offers only "Remove application and data?", and
answering yes deletes both. The CLI treats data removal as opt-in - the default
removes only the Application and leaves the volume. A TUI user gets no such
choice, so the two frontends disagree on the single most destructive decision in
the product.

Give the TUI remove screen the same choice the CLI flag provides: remove the
Application and keep its data, or remove both. Keeping data is the default,
matching the CLI. The confirmation wording must say which of the two will happen,
so the user is never told "and data" when data is being kept.

No spine is needed here: the underlying remove is one guard and one call, the
same reasoning that left Funnel disable without a spine in 05. This ticket only
closes the frontend behaviour gap.

**Blocked by:** None - can start immediately.

**Status:** ready-for-agent

- [ ] The TUI remove screen offers a choice between removing only the Application and removing it with its data
- [ ] The default choice keeps the data, matching the CLI's default
- [ ] The confirmation text names exactly what the selected choice will destroy
- [ ] Removing with keep-data leaves the Application's volume in place; removing with data deletes it
- [ ] CLI remove behaviour and flag default are unchanged
- [ ] Integration tests pass, with the TUI remove driver updated for the new choice in the same change
