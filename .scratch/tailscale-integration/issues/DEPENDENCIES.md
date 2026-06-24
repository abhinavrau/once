# Tailscale Integration — Issue Dependency Graph

Arrows point from a blocker to the issue it unblocks (`A --> B` = B is blocked by A).
Generated from the `## Blocked by` section of each issue.

```mermaid
graph TD
    01["01 · Headscale harness + control seam"]
    02["02 · tailscale enable/disable + tsdproxy container"]
    03["03 · App tsdproxy.* labels + retrofit roll"]
    04["04 · Lookup API + status + list tailnet URL"]
    05["05 · Admin socket daemon + once-admin nginx"]
    06["06 · Funnel toggle + FunnelExpiresAt"]
    07["07 · Funnel auto-expiry daemon"]
    08["08 · TUI global Tailscale settings form"]
    09["09 · TUI details URLs + Funnel sub-form"]
    10["10 · teardown full cleanup"]
    11["11 · SaaS smoke pass"]

    01 --> 02
    02 --> 03
    02 --> 04
    02 --> 05
    02 --> 08
    02 --> 10
    03 --> 04
    03 --> 05
    03 --> 06
    04 --> 09
    04 --> 11
    05 --> 10
    06 --> 07
    06 --> 09
    06 --> 11
    07 --> 09
    07 --> 11

    classDef done fill:#2e7d32,stroke:#1b5e20,color:#fff;
    classDef ready fill:#1565c0,stroke:#0d47a1,color:#fff;
    classDef needsinfo fill:#f9a825,stroke:#f57f17,color:#000;

    class 01,02,03,04 done;
    class 05,06,07,08,09,10 ready;
    class 11 needsinfo;
```

## Legend

| Color | Status |
|-------|--------|
| 🟩 Green | `done` |
| 🟦 Blue | `ready-for-agent` |
| 🟨 Yellow | `needs-info` |

## Status snapshot

| # | Issue | Status | Blocked by |
|---|-------|--------|-----------|
| 01 | Headscale harness + control seam | done | — |
| 02 | `once tailscale enable/disable` + tsdproxy container | done | 01 |
| 03 | App `tsdproxy.*` labels + retrofit roll | done | 02 |
| 04 | Lookup API + `status` + `list` tailnet URL | done | 02, 03 |
| 05 | Admin socket daemon + `once-admin` nginx | ready-for-agent | 02, 03 |
| 06 | Funnel toggle + `FunnelExpiresAt` | ready-for-agent | 03 |
| 07 | Funnel auto-expiry daemon | ready-for-agent | 06 |
| 08 | TUI global Tailscale settings form | ready-for-agent | 02 |
| 09 | TUI details URLs + Funnel sub-form | ready-for-agent | 04, 06, 07 |
| 10 | `once teardown` full cleanup | ready-for-agent | 02, 05 |
| 11 | SaaS smoke pass | needs-info | 04, 06, 07 |
| 14 | Validate OAuth credentials before enabling | needs-triage | — |
| 15 | Fetch tailnet domain suffix on enable | needs-triage | 14 |
| 16 | Headscale control server enable (model + CLI) | needs-triage | — |
| 17 | TUI Headscale control server fields | needs-triage | 16 |
