# 01 - TUI surfaces settings-deploy failures honestly

**What to build:** A user who changes an Application's Configuration in the TUI
and hits a failure currently sees nothing: the settings screen navigates back to
the app as if the change succeeded. Make the failure visible, and make the
in-memory Configuration tell the truth afterwards.

Three defects, one theme - the TUI lying about failure:

1. The deploy result is discarded. A failed roll navigates away silently, so the
   user believes a setting took effect when it did not. Surface the error on the
   settings screen the way other TUI errors are surfaced.
2. On failure the Application keeps the Configuration that just failed to apply,
   so the screen and the dashboard show settings that are not running anywhere.
   Restore the previous Configuration when the roll fails.
3. `ErrTailscaleNotEnabled` carries CLI syntax in its message text, so a TUI user
   who trips it is told to run a CLI command. Strip the affordance from the
   sentinel and leave the CLI to add its own wording. The TUI already has its own
   phrasing for this condition in the exposure and Funnel forms.

There is a characterization test pinning the navigate-away behaviour, with a
comment stating it documents current behaviour rather than intent. Rewrite it to
assert the error is surfaced.

This ticket is deliberately confined to honesty about failure. The related gap -
that the TUI never validates Configuration before applying it - is closed by 04,
where validation moves inside the Apply Settings spine rather than being called
by each frontend.

**Blocked by:** None - can start immediately.

**Status:** ready-for-agent

- [ ] A settings change whose roll fails shows the error on the settings screen instead of navigating to the app
- [ ] After a failed roll, the Application's in-memory Configuration is the one that was running before the change
- [ ] A settings change that succeeds still navigates to the app as before
- [ ] `ErrTailscaleNotEnabled`'s message names the condition without naming a CLI command
- [ ] The CLI still tells the user to run `once tailscale enable` when it reports that condition
- [ ] The characterization test asserting navigate-on-error is replaced by one asserting the error is surfaced
