# 07 - `--json` on read-only commands

**What to build:** An LLM agent or script branches on structured state instead of parsing prose. `list`, `tailscale status`, and per-app status accept `--json` and emit stable, structured output including each app's exposure and tailnet state. Human-facing output is unchanged when the flag is absent. Mutating commands get no JSON mode (out of scope by spec).

**Blocked by:** 02 - Tailnet exposure and routing exclusivity.

**Status:** done

- [x] `list --json` includes each app's name, host, exposure, and running state
- [x] `tailscale status --json` includes enablement and per-app service state
- [x] Per-app status supports `--json`
- [x] Output is valid JSON parseable by `jq`; prose output unchanged without the flag

## Comments

No per-app status command existed, so this adds `once status <host>` with the
same prose line `list` prints and the same JSON object shape (`appReport`)
under `--json`. The app JSON also carries `url`, `tailnetUrl`, and
`funnelExpiresAt`, and `tailscale status --json` carries `domainSuffix`:
an agent branching on exposure needs to see an active Funnel and to
distinguish "Magic DNS not ready" from "no services".
