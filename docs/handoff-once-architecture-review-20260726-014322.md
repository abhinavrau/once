# Handoff - Once architecture review

**Repo:** `/home/arau/code/once` (branch `main`, at `2ecb6b2`)
**Next session focus:** continue the architecture review - candidates 02 through 07. Candidate 01 is finished.

---

## What happened

Ran `/mattpocock-skills:improve-codebase-architecture`. Scoped to the hot spots of the last 60 commits (Tailscale/Exposure, the `once` label, the `command`/`ui`/`docker` seams), produced 7 deepening candidates as an HTML report, then took candidate 01 through `/grilling` and `/to-tickets`.

**Candidate 01 is done and needs nothing further** - decided, ticketed, glossary updated. Do not re-open it.

**Candidates 02-07 have not been explored.** That is the work.

---

## Artifacts

| Path | What |
|---|---|
| `/tmp/architecture-review-20260725-182048.html` | The full report, all 7 candidates with before/after diagrams. **Volatile - it is in `/tmp`.** Index below survives if it is gone. |
| `.scratch/namespace-workflow-spine/issues/01..05` | Candidate 01's tickets, all `ready-for-agent`. Frontier: 01 and 02. |
| `CONTEXT.md` (uncommitted) | Gained an **Apply Settings** glossary entry and a clarifying note on **Update**. |
| `docs/adr/0002..0007` | Read these before touching Tailscale. 0005 and 0007 constrain candidates below. |

Nothing is committed. `CONTEXT.md` is modified; `.scratch/namespace-workflow-spine/` is untracked.

---

## Candidate index (02-07)

Strength ratings and evidence anchors, so the review can continue if the HTML is gone. All file:line refs were verified by hand this session unless noted.

**02 - One module owns the `once` label.** *Strong.* Four hand-written codec pairs (`application_settings.go:74,92`, `proxy.go:45,51`, `volume.go:25,31`, `tailscale.go:168,174`). Unknown-field preservation from commit `3e47d59` exists on `ApplicationSettings` only; `unknownFields()` (`application_settings.go:211`) is generic over `reflect.Type` and has one caller. `TailscaleSettings` is rewritten by two *read* paths (`tailscale.go:357`, `:385` both call `persistSettings`) and has grown four optional fields - same bug shape, triggered by a status read. Secondary: `extra` is unexported so `Equal` ignores it, and the TUI short-circuits deploys on `Equal`.

**03 - Put a seam on the Docker client.** *Strong.* `Namespace.client` is a concrete `*client.Client` (`namespace.go:31`). Only two narrow interfaces exist in the package (`logsClient` at `logs.go:37`, `statsClient` at `scraper.go:35`) and they are the only unit-tested Docker paths. `namespace_test.go:38` uses a nil-pointer panic as its test seam. **Sequencing decision already made: this lands *after* the candidate-01 spine tickets**, because the spine leaves one orchestration to test instead of two.

**04 - Resolve Tailscale mode and daemon once.** *Worth exploring.* Mode resolution exists twice - `effectiveMode` (`tailscale.go:460`) and `Enable` (`:240`), and `effectiveMode`'s own comment admits it mirrors the other. Four identical `LoadSettings` + `daemonFor` binder wrappers (`tailscale_status.go:26`, `tailscale_names.go:30`, `tailscale_verify.go:34`, `tailscale.go:370`). Control seam is a hidden env-var global with 8 branch points across 5 files and 3 packages, two of which leak into `command` and `ui`.

**05 - Turn `baseURL` into an adapter.** *Worth exploring.* No seam on the Tailscale HTTP API; `http.DefaultClient.Do` raw at `tailscale_validate.go:228,411`. `baseURL string` is declared in **7** function signatures (verified count - an earlier subagent said 12, it is 7) with exactly one production value, existing only so tests can redirect it. Three hand-rolled httptest servers. **ADR-0005 constraint: any adapter sits above the metadata-only `http.RoundTripper` and must never read bodies.**

**06 - Address Form fields by key, not index.** *Worth exploring.* `form.go:404` reads fields by int through unchecked type assertions; every form declares its own `iota` block; tab order depends on field count *and* whether `SetActionButton` was called (`form.go:508`). `submitIfValid` (`:471`) rejects silently by refocusing, so the integration driver - which only reads rendered text - hangs. `integration/ui_test.go:206` burns 3 of the suite's 10 minutes before timing out. AGENTS.md documents this hazard; fixing it would retire that note.

**07 - One progress adapter.** *Speculative.* One `DeployProgressCallback`, four incompatible adapters plus two silent `nil` paths (`ui/settings.go:292`, `background/runner.go:198`). The TUI loses download percentage on exactly the operations where the CLI keeps it. `command` imports `ui` to borrow the progress widget (`cli_progress.go:10`).

---

## Decisions from this session that constrain what comes next

- **`internal/docker` will import `internal/service`** (candidate 01, ticket 05). Acyclic - `service` imports nothing internal. This invalidates the "docker is nearly a leaf" premise if you reach for it while arguing candidate 03 or 04.
- **Warnings convention is `([]string, error)`**, matching `Namespace.EnableTailscale`. Any new spine follows it rather than inventing a second shape.
- **Frontend-specific error wording is correct and stays.** Sentinels are affordance-free; each frontend supplies its own advice. Do not propose collapsing that into one message - it was considered and rejected with reasons.
- **Candidate 03 is sequenced after candidate 01's tickets.** Do not re-argue it as "do this first".
- `ErrTailscaleNotEnabled` loses its CLI syntax in ticket 01.

---

## Method notes

- **Verify subagent claims before publishing them.** Explore agents produced good structural findings but at least one inflated number (the `baseURL` count). Every file:line in the report was re-checked with `grep`/`diff` first. Keep doing that.
- The `/improve-codebase-architecture` flow is: explore -> HTML report -> user picks a candidate -> `/grilling` one question at a time -> domain side effects inline to `CONTEXT.md` -> `/to-tickets`.
- Grilling worked well by looking facts up rather than asking. Only put genuine *decisions* to the user; evidence settled several apparent questions outright (for example the TUI's `removeData: true` turned out to be disclosed by its own confirmation prompt, so it was never a bug).

---

## Repo gotchas

- **Shell is zsh** - `grep --include=*.go` fails on glob expansion. Quote it: `--include="*.go"`.
- **Never use the em dash.** Use `-`. (Five pre-existing ones in `CONTEXT.md` are the author's, left alone.)
- Never add an agent name as commit co-author. Never hand-edit `CHANGELOG.md`.
- Use `gh-axi` not `gh`; `chrome-devtools-axi` for browser work.
- Integration suite has a 10-minute total budget; one hung test consumes it all and reports as a timeout panic. Read AGENTS.md's testing notes before touching `integration/`.
- Tailscale SaaS OAuth paths cannot run in CI - cover them with httptest unit tests.
- **Ponytail mode was active** (`SessionStart` hook, level `full`): prefer the laziest solution that works, stop at the first rung that holds. It shaped several recommendations, notably rejecting spines that would be pass-throughs.
- Run `deja "<query>"` before debugging or re-implementing - past sessions are indexed locally.

---

## Suggested skills

- **`mattpocock-skills:improve-codebase-architecture`** - only if the user wants a *fresh* review. The existing report already covers 02-07; re-running it would redo work.
- **`mattpocock-skills:grilling`** - the main one. Use it on whichever candidate the user picks next, exactly as candidate 01 was handled: one question at a time, recommendation on each, look facts up rather than asking.
- **`mattpocock-skills:codebase-design`** - load it for the vocabulary before grilling. Use *module, interface, implementation, depth, deep, shallow, seam, adapter, leverage, locality* exactly; never "component", "service", "API", "boundary".
- **`mattpocock-skills:domain-modeling`** - if a deepened module gets named after a concept absent from `CONTEXT.md`, or if a candidate is rejected for a load-bearing reason worth an ADR.
- **`mattpocock-skills:to-tickets`** - to publish. Tracker is local markdown: `.scratch/<feature-slug>/issues/<NN>-<slug>.md`, `**Status:** ready-for-agent`, blockers listed by number. See `docs/agents/issue-tracker.md`.

## Suggested opening move

Ask which of 02-07 to take. If the user has no preference, recommend **02** - it is the only remaining candidate where a bug that was already fixed once is sitting unfixed on three sibling types, with a live trigger through `persistSettings`.
