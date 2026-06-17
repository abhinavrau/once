Status: ready-for-agent

# TUI global Tailscale settings form (`t` key)

## Parent

- PRD: `.scratch/tailscale-integration/PRD.md`
- ADR: `docs/adr/0001-tailscale-integration.md`

## What to build

A dashboard-level overlay for configuring Tailscale globally, wired to the same enable/disable machinery the CLI uses.

- Add a global key binding (`t` / `Shift+T`) on the dashboard that opens a **Global Tailscale Settings Form** overlay with fields: Enable Tailscale, OAuth Client ID, OAuth Client Secret.
- Submitting with Tailscale enabled runs the enable flow (boot `once-tsdproxy` + `once-admin`, store credentials, roll apps to add labels); disabling runs the disable flow. This reuses the slice 02/03/05 enable/disable code paths — the form is a thin front-end over them.
- Follows the existing settings-overlay conventions (the `SettingsSection` interface / form-base components), but is dashboard-global rather than per-app.

## Acceptance criteria

- [ ] Pressing `t` on the dashboard opens the Global Tailscale Settings Form overlay
- [ ] The form has Enable Tailscale, OAuth Client ID, and OAuth Client Secret fields, pre-populated from current settings when already enabled
- [ ] Submitting enables/disables Tailscale via the existing CLI-shared code paths (no duplicated lifecycle logic)
- [ ] The overlay follows the existing form/key-help conventions and can be dismissed with `esc`

## Blocked by

- `.scratch/tailscale-integration/issues/02-tailscale-enable-disable-tsdproxy-container.md`
