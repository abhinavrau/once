# 08 - TUI: tagOwners step and the Merged Policy diff screen

**What to build:** The production TUI screens the prototype validated, lifted
from `cmd/prototype-tailnet-policy/main.go` (branch
`prototype/tailnet-policy`) into `internal/ui/` beside `tailscale_form.go`
and the custody-flow pattern (`custody.go`).

Carry over what the prototype settled (ADR-0017 amendment):

- tagOwners screen: three numbered steps, both entry shapes labelled, one
  copy path shared by the visible button and ctrl+p, skip for existing tags.
- Diff screen: summary line, viewport auto-scrolled to the first addition,
  overview-ruler markers on the scrollbar, soft-wrapped added lines, copy
  control outside the scrolling region, exact-height chrome accounting
  (the prototype's diffChrome + TestDiffScreenFitsSmallTerminals pattern).
- Topology as the credential form's first field with a live scope list.
- Continue bottom-right, no Cancel button, `Copied ✓` state.

**Blocked by:** 06, 07, and 09 for the button layout.

**Status:** done

- [x] Both screens fit and stay usable at 80x24; a size test pins the chrome like the prototype's.
- [x] The copy control is a named click target the integration form drivers can assert (`integration/ui_test.go` conventions).
- [x] The credential screen does not overflow the 24-row floor - coordinate with `.scratch/tailnet-topology/issues/07-enable-form-overflows-small-terminals.md` (scroll or collapse the guidance steps; the prototype's version measured 38 rows).
- [x] VHS-style verification at 24/46/60 rows, or an equivalent height-pinned render test.

## Comments

Lifted into internal/ui/tailscale_policy_screens.go (tagOwnersScreen,
policyDiffScreen) hosted by TailscaleForm as a three-screen flow: fresh
enable opens on tagOwners (s skips, esc closes), the credential form follows
(topology first, live scope list from TopologyFacts.OAuthScopes - now its
first production consumer, spelled out with :write and the ADR-0017 read
pair), and a refused enable lands on the Merged Policy diff built from the
review (PolicyReview gained Original so the diff renders the user's own
document as context). Diff Continue re-runs the same enable; esc returns to
the form where the refusal message waits. ctrl+p and the visible copy
control share one payload per screen; copy-policy/continue are named click
targets the integration driver exercises.

24-row floor: guidance alone was not enough (the bordered fields measure 24
rows by themselves), so ui.Form gained opt-in SetCompact (inline radios, no
separator rows) and the credential screen collapses by measurement: full
guidance -> no guidance -> compact+tight chrome. Height pinned at 24/46/60
by TestTailscaleForm_CredentialScreenFitsThe24RowFloor and
TestPolicyDiffScreenFitsSmallTerminals (exact-height, policyDiffChrome=15).
Fixed along the way: trimLineEnds now trims pad spaces interleaved with
trailing zero-width escapes, which had shifted right-aligned button borders
by one cell.
