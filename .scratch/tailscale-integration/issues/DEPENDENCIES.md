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
    12["12 · Enable Tailscale with no apps"]
    13["13 · Per-app tailnet opt in/out"]
    14["14 · Validate OAuth credentials on enable"]
    15["15 · Fetch tailnet domain suffix"]
    16["16 · Headscale control server enable"]
    17["17 · TUI Headscale control server fields"]

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
    14 --> 15
    16 --> 17

    classDef done fill:#2e7d32,stroke:#1b5e20,color:#fff;
    classDef ready fill:#1565c0,stroke:#0d47a1,color:#fff;
    classDef needsinfo fill:#f9a825,stroke:#f57f17,color:#000;
    classDef triage fill:#6d4c41,stroke:#4e342e,color:#fff;

    class 01,02,03,04,05,06,07,08,09,10,12,13 done;
    class 11 needsinfo;
    class 14,15,16,17 ready;
```

## Legend

| Color | Status |
|-------|--------|
| 🟩 Green | `done` |
| 🟦 Blue | `ready-for-agent` |
| 🟨 Yellow | `needs-info` |
| 🟫 Brown | `needs-triage` |

## Status snapshot

| # | Issue | Status | Blocked by |
|---|-------|--------|-----------|
| 01 | Headscale harness + control seam | done | — |
| 02 | `once tailscale enable/disable` + tsdproxy container | done | 01 |
| 03 | App `tsdproxy.*` labels + retrofit roll | done | 02 |
| 04 | Lookup API + `status` + `list` tailnet URL | done | 02, 03 |
| 05 | Admin socket daemon + `once-admin` nginx | done | 02, 03 |
| 06 | Funnel toggle + `FunnelExpiresAt` | done | 03 |
| 07 | Funnel auto-expiry daemon | done | 06 |
| 08 | TUI global Tailscale settings form | done | 02 |
| 09 | TUI details URLs + Funnel sub-form | done | 04, 06, 07 |
| 10 | `once teardown` full cleanup | done | 02, 05 |
| 11 | SaaS smoke pass | needs-info | 04, 06, 07 |
| 12 | Enable Tailscale with no apps | done | — |
| 13 | Per-app tailnet opt in/out | done | — |
| 14 | Validate OAuth credentials before enabling | ready-for-agent | — |
| 15 | Fetch tailnet domain suffix on enable | ready-for-agent | 14 |
| 16 | Headscale control server enable (model + CLI) | ready-for-agent | — |
| 17 | TUI Headscale control server fields | ready-for-agent | 16 |
