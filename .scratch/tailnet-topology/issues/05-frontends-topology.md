# 05 - Frontends: `--topology`, per-topology facts, older-install message

**What to build:** Review candidate 04 plus the user-facing topology choice.
CLI: `once tailscale enable --topology services|nodes` (default: none given
-> `resolveTopology`, i.e. keep stored or `nodes` when fresh); `--mode` help
says Services-only. `once tailscale status` and `once list` render whatever
the facade reports (topology, per-app tailnet URL, Funnel URL) and build no
`TailscaleSettings{...}` of their own. TUI: the enable form gains a
Topology selector; the Mode radio renders only when the selected topology's
facts offer mode choices; the intro text, scopes list, and `ctrl+p` policy
snippet come from the adapter's static facts. Zero vendor names, mode lists,
scope lists or ACL text typed into `internal/ui` or `internal/command`.

Older install: any `once tailscale *`, `once list`, the dashboard and the
settings form show the facade's "setup is from an older version of Once;
re-run `once tailscale enable`" message instead of a stack of inspect
errors.

Keep the ADR-0007 `--mode host` notice for Services fresh enables (it is
adapter-supplied prose now).

**Blocked by:** 04.

**Status:** ready-for-agent

- [ ] `--topology` flag exists with the two values, validated by the docker
      package (`ValidateTailnetTopology`), and the TUI selector uses the same
      values via named field keys (integration `ui_test.go` driver clicks
      `field:topology`)
- [ ] `grep -rn "docktail\|tsdproxy\|sidecar\|tag:once\|auth_keys" internal/ui
      internal/command` returns only test fixtures
- [ ] `once tailscale status` prints the topology and, per app, the tailnet
      URL and Funnel URL from one facade read; no `TailscaleSettings{`
      literal outside `internal/docker` and its tests
- [ ] The older-install message appears once, verbatim, in CLI status, list,
      and the dashboard when the label lacks a topology
- [ ] Integration ui tests updated for the new field key; suite green under
      `flock`

**Follow-up (orchestrator):** `TestUITailscaleFormEnablesViaControlSeam` was
failing deterministically at 120x40: the enable form with the Topology radio
plus the Services Mode radio runs to ~46 rows, `OverlayCenter` clips an
overlay taller than the window at both ends, so the form's title and help
line vanished and the test's "wait until `close` disappears" fell through
before submit. Fixed the visible causes: the facts' `SetupIntro` no longer
carries the CLI-only step 3 (secret env var + `--client-id`) - the CLI's
enable help already says that, and it was wrong prose inside the TUI, whose
Client ID/Secret fields sit right below; the test now runs at 120x50 and
asserts the form's help is visible before submitting, so a future overflow is
a named assertion. Open UX question, not decided here: the form still
overflows terminals under ~46 rows (min size is 80x24; before this ticket it
overflowed under ~38). Options: compact radios (label and options on one
row), a scrollable overlay, or a shorter intro. Needs a product call.
