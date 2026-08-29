# 07 - Enable outcomes: proceed, warn, refuse, stale

**What to build:** Wire the four validated end states into
`once tailscale enable` (`internal/command/tailscale.go`), exactly as the
prototype demonstrated (ticket 03, `nextView` in the prototype's main.go):

- Nothing to add: proceed, no diff shown.
- Missing `funnel` nodeAttr: warn naming that only Funnel breaks, proceed.
- Missing 443 grant or `autoApprovers.services` (Services topology): refuse,
  naming the section and what it breaks. Nothing gets installed.
- ETag changed between merge and install: report the edit, do not proceed.

**Blocked by:** 06.

**Status:** done

- [x] All four outcomes reachable in both CLI and TUI paths; the CLI never prompts (ADR-0004/0017: script and terminal behave the same).
- [x] Refusal prints the Merged Policy (or where to get it) plus "apply this, then re-run".
- [x] The warn path's wording names Funnel as the only casualty.
- [x] The stale path re-checks the captured ETag immediately before install.

## Comments

The outcomes live in the shared lifecycle, not in either frontend:
`Namespace.EnableTailscale` runs `reviewTailnetPolicy` right after the
credential probe (skipped on the control seam, like the probes it extends)
and classifies via `PolicyReview.blocksInstall()`/`warning()`:

- Nothing to add: proceed, no policy output at all.
- Only `nodeAttrs` missing: a warning naming Tailscale Funnel as the only
  thing that breaks, then proceed; it rides the existing warnings channel,
  so the CLI prints `Warning:` and the TUI keeps the form open showing it.
- Missing 443 grant or `autoApprovers.services`: `PolicyRefusedError`
  (carrying the review) before anything boots. The CLI prints the Merged
  Policy plus "apply ... then re-run `once tailscale enable`"; the TUI shows
  the refusal and turns ctrl+p into a Merged Policy copy, hint line included.
- Stale: `requireUnchangedPolicy` re-reads the ETag immediately before
  `tailscale.Enable` (after proxy Setup, before any tailnet install) and
  stops with `ErrPolicyChangedDuringEnable` when it moved. A read that newly
  degrades to the fallback (scope revoked mid-flight) proceeds - the scope
  is an enhancement, never a precondition.

The fallback review (no `policy_file:read`) also surfaces here: a warning
naming the scope and pointing at `once tailscale policy`, with the flow
proceeding exactly as before ADR-0017. No prompts anywhere; script and
terminal behave the same. Covered by unit tests in the docker, command, and
ui packages; the control-seam skip is exercised by the existing enable
integration tests.

**Review fixes (2026-08-29):**

- **443 grant is Services-only.** The refuse clause's "Missing 443 grant ...
  (Services topology): refuse" now holds in the merge engine too: under Nodes
  no grant ACL test runs, no grants entry is merged, and a missing grant never
  blocks the install. Rationale: NodesPolicySnippet (and its pinning test) -
  a tagged node is an ordinary tailnet device under the tailnet's existing
  rules, not a Service needing its own approval and access-grant step; ADR-0017's
  grant refusal is motivated entirely by Services symptoms. This supersedes
  the ADR-0017 amendment's "Nodes merges `grants` and `nodeAttrs` only"
  sentence on the grants half: Nodes merges the funnel nodeAttr only.
- **Stale-path placement and wording.** The ETag re-check stays immediately
  before `tailscale.Enable` (after proxy Setup/EnsureNetwork): moving it ahead
  of Setup would widen the check-to-install window by the whole proxy boot,
  defeating "re-checks the captured ETag immediately before install". Since
  the proxy may therefore already be up when the stop fires,
  `ErrPolicyChangedDuringEnable` no longer claims "nothing was installed";
  it says the tailnet install was stopped before it began.
- **TUI outcome visibility after diff Continue.** Every enable finish resets
  the overlay to the credential screen unless it is a refusal (which builds a
  fresh diff), so a stale stop, credential error, or warn-and-proceed after
  the diff's Continue renders instead of leaving the old diff on screen.
