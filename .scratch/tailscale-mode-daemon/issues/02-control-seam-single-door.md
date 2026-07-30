# 02 - One door for the hidden control seam

**What to build:** The hidden headscale control seam stops being a global that
eight scattered branch points read for themselves, two of them from the
frontends. After this ticket, every read of the seam's env vars goes through
one accessor inside the Docker package, and no code outside that package knows
the seam exists.

One private accessor returns the pre-minted auth key, the control URL, and
whether the seam is active; the boolean helper delegates to it. The sidecar
boot's raw env reads go through the accessor like everyone else. The four
SaaS-skip branches (credential probe, tailnet-membership check, cloud approval
check, service verify) keep calling the boolean helper unchanged - gathering
those behind the Tailscale API seam belongs to the tailscale-api-adapter
feature.

The credential-suppression rule the CLI and TUI each re-implement (under the
seam, OAuth credentials are unused, so don't store what the user typed) is
absorbed into the Enable Tailscale workflow, next to the tag normalization it
already does. Both frontends then always pass what they collected, their seam
branches are deleted, and the exported seam predicate is deleted with them -
the Tailscale interface shrinks by one symbol and the seam stops leaking.

Fix along the way: the settings Tag doc comment claims the tag is "Empty on
the headscale control seam", but frontends have always stored it; the tag is
merely unused there. Correct the comment (Tag is deliberately NOT suppressed -
preserve behavior).

The seam is provider-shaped (a future VPN provider gets its own control
plane story), so the single accessor is the door a provider seam would later
open through; do not build more than the accessor now.

**Blocked by:** 01 - Resolve the Tailscale mode once per enable (both rewrite
the Enable Tailscale workflow's settings preamble).

**Status:** ready-for-agent

- [ ] Exactly one function reads the control-seam env vars; the active-check
      helper and the sidecar boot both go through it (unit-tested with the
      existing Setenv pattern)
- [ ] Enabling under the seam from either frontend stores no OAuth
      credentials in the once label, with the suppression living in the
      Enable Tailscale workflow, not the frontends
- [ ] The exported seam predicate is gone and no code outside the Docker
      package mentions the seam
- [ ] The TUI control-seam integration test still passes with the form's
      suppression branch deleted, and gains an assert that the stored label
      carries no credentials
- [ ] The four SaaS-skip branch sites are textually untouched apart from what
      the accessor rename forces (they belong to tailscale-api-adapter)
- [ ] The settings Tag doc comment no longer claims the tag is empty under
      the seam
