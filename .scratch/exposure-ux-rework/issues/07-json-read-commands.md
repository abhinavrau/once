# 07 - `--json` on read-only commands

**What to build:** An LLM agent or script branches on structured state instead of parsing prose. `list`, `tailscale status`, and per-app status accept `--json` and emit stable, structured output including each app's exposure and tailnet state. Human-facing output is unchanged when the flag is absent. Mutating commands get no JSON mode (out of scope by spec).

**Blocked by:** 02 - Tailnet exposure and routing exclusivity.

**Status:** ready-for-agent

- [ ] `list --json` includes each app's name, host, exposure, and running state
- [ ] `tailscale status --json` includes enablement and per-app service state
- [ ] Per-app status supports `--json`
- [ ] Output is valid JSON parseable by `jq`; prose output unchanged without the flag
