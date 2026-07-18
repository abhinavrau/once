# 03 - TUI exposure: radio, suggestions, settings section

**What to build:** The TUI presents the same three-way exposure decision the CLI offers. The install form gains an exposure radio, preselected to tailnet when Tailscale is enabled and local otherwise, with the host field prefilled with the derived suggestion for the currently selected exposure (updating as the selection changes; public shows no default and requires input). The per-app settings menu replaces the Tailscale section with an Exposure section that switches an installed app's exposure, keeping the Funnel section as is. All touched form drivers in the integration suite are updated in the same change, per the standing testing note about silently hanging drivers.

**Blocked by:** 02 - Tailnet exposure and routing exclusivity.

**Status:** ready-for-agent

- [ ] Install form shows the exposure radio with context-sensitive preselection
- [ ] Host field prefills the derived suggestion per selected exposure; public requires explicit input
- [ ] A full TUI install succeeds for each of the three exposures
- [ ] Settings menu offers an Exposure section that switches exposure on an installed app
- [ ] The old Tailscale toggle section is gone; the Funnel section remains
- [ ] TUI integration drivers updated in the same change and passing
