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

**Status:** ready-for-agent

- [ ] All four outcomes reachable in both CLI and TUI paths; the CLI never prompts (ADR-0004/0017: script and terminal behave the same).
- [ ] Refusal prints the Merged Policy (or where to get it) plus "apply this, then re-run".
- [ ] The warn path's wording names Funnel as the only casualty.
- [ ] The stale path re-checks the captured ETag immediately before install.
