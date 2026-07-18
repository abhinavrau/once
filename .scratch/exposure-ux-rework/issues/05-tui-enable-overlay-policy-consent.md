# 05 - TUI enable overlay with policy consent

**What to build:** Full Tailscale setup without leaving the TUI, sharing one consent seam with the CLI. The enable overlay reduces to Bootstrap Credential ID and secret fields with the tag prefilled to `tag:once`, then renders the same planned policy diff (with the GitOps warning) and an explicit Apply/Decline choice; Decline shows the HuJSON snippet in a copyable view. Choosing tailnet exposure in the install form before Tailscale is enabled offers opening this overlay inline instead of bouncing the user to the CLI. Form drivers updated in the same change.

**Blocked by:** 03 - TUI exposure radio; 04 - Bootstrap-only `tailscale enable`.

**Status:** ready-for-agent

- [ ] Overlay shows only bootstrap ID/secret and a prefilled tag field
- [ ] Policy diff renders in the overlay with Apply and Decline
- [ ] Decline shows the copyable snippet; nothing is written and no Policy Credential is minted
- [ ] Selecting tailnet exposure with Tailscale disabled offers the enable overlay inline
- [ ] TUI integration drivers updated in the same change and passing
