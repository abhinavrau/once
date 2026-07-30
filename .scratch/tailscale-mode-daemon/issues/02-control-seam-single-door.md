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

**Status:** done

- [x] Exactly one function reads the control-seam env vars; the active-check
      helper and the sidecar boot both go through it (unit-tested with the
      existing Setenv pattern)
- [x] Enabling under the seam from either frontend stores no OAuth
      credentials in the once label, with the suppression living in the
      Enable Tailscale workflow, not the frontends
- [x] The exported seam predicate is gone and no code outside the Docker
      package mentions the seam
- [x] The TUI control-seam integration test still passes with the form's
      suppression branch deleted, and gains an assert that the stored label
      carries no credentials
- [x] The four SaaS-skip branch sites are textually untouched apart from what
      the accessor rename forces (they belong to tailscale-api-adapter)
- [x] The settings Tag doc comment no longer claims the tag is empty under
      the seam

## Comments

**2026-07-30** - branch `tailscale-mode-daemon-02-control-seam-single-door`,
worktree `/home/arau/code/once/.claude/worktrees/agent-afd50be72ab73d630`.

`controlSeam() (authKey, controlURL, ok)` is now the only reader of
`ONCE_TAILSCALE_AUTH_KEY`/`ONCE_TAILSCALE_CONTROL_URL`; `controlSeamActive`
delegates to it and the sidecar boot's raw `os.Getenv` pair goes through it.
Credential suppression moved into `EnableTailscale` as
`settings.suppressUnusedCredentials()`, beside the tag normalization and before
the only label write, so both frontends now always pass what they collected and
`TailscaleControlSeamActive` is deleted. Zero seam mentions remain in
`internal/ui` and `internal/command` (the integration package still sets the env
vars to drive the headscale harness). The four SaaS-skip sites
(`tailscale_validate.go:51`, `:118`, `tailscale_status.go:42`,
`tailscale_verify.go:30`) are untouched - no rename was forced.

Review turned up one real defect and fixed it: the Tag doc comment's replacement
claim ("unused on the seam, but docktail stamps it") was false in both halves -
under the seam `create` leaves docktail's env nil so `DEFAULT_SERVICE_TAGS` is
never passed, while `validateHostModePreflight` reads `settings.Tag` at
`tailscale_validate.go:110`, *before* its seam skip. The comment now says the tag
is stored on the seam too, where only the host-mode pre-flight reads it. Also
fixed two comments the change had made self-contradictory (`EnableTailscale`
still claiming it boots "with the given credentials"; the TUI's `runEnable`
claiming the credential probe always runs).

`make test` and `make lint` pass; the integration suite was not run locally (the
coordinator runs it) but compiles and vets clean.

- 2026-07-30 (coordinator): merged into arch-review-wave-2-tickets at ec83fc0. go vet, make test, make lint, and the full integration suite (489s) green on the merged tree. Done.
