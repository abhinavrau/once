# Handoff - Once architecture review, wave 2 fan-out

**Repo:** `/home/arau/code/once` (branch `main`, at `0da2dc4`, clean)
**Next session focus:** fan out the 4 wave-2 frontier tickets to parallel
implement agents, merge, verify, close out. The recipe below is exactly what
worked for wave 1.

---

## What happened before this

- Architecture-review candidates 02-07 were grilled and ticketed by parallel
  agents. ADRs 0008-0012 and new CONTEXT.md glossary entries landed on main.
  Candidate 07 was rejected as architecture (ADR-0012) and shrank to one
  wiring ticket.
- Wave 1 (the 6 then-unblocked tickets) was implemented by 6 parallel Opus
  agents in isolated worktrees, merged via `arch-review-frontier-tickets`
  into main (fast-forward to `0da2dc4`). Unit tests, lint, and the full
  integration suite were green on the merged tree (437s run).
- Features now complete: docker-client-seam, progress-adapter.
- The whole 6-branch merge had exactly one conflict (the host-mode preflight
  call in `internal/docker/namespace.go`); tight per-agent scope guards are
  why. Keep writing them.

## All remaining review tickets - 7, across 4 features

Complete ledger of what is left from architecture-review candidates 02-07.
Everything not listed here is done (docker-client-seam and progress-adapter
are complete; so are ticket 01 of tailscale-mode-daemon, tailscale-api-adapter,
and form-fields-by-key). All paths under `.scratch/`.

**once-label-module** (candidate 02, ADR-0008):
- `once-label-module/issues/01-label-codec-module.md` - **wave 2**, unblocked
  (its blocker docker-client-seam/01 is done). New `internal/docker/label.go`
  generic codec + `ApplicationSettings` moved onto it; `application_settings.go`.
- `once-label-module/issues/02-protect-remaining-labels.md` - **wave 3**,
  blocked by its 01. Moves Proxy/Volume/Tailscale settings onto the codec and
  grafts unknown fields on `Enable`'s rebuild - closes the live
  `TailscaleSettings` field-destruction bug, the review's highest-value fix.

**tailscale-mode-daemon** (candidate 04, ADR-0010):
- `tailscale-mode-daemon/issues/02-control-seam-single-door.md` - **wave 2**,
  unblocked (its blocker, ticket 01, is done). `tailscale.go` control-seam
  reads behind one accessor, credential suppression absorbed into
  `EnableTailscale`, deletes the `command/tailscale.go` and
  `ui/tailscale_form.go` copies and the `TailscaleControlSeamActive` export.
- `tailscale-mode-daemon/issues/03-session-binder.md` - **wave 4**, blocked by
  once-label-module/02 (both rewrite the `DomainSuffix`/`WaitForFunnelActive`
  persist call sites). One session function replacing the five
  `LoadSettings`+`daemonFor` binder pairs.

**tailscale-api-adapter** (candidate 05, ADR-0011):
- `tailscale-api-adapter/issues/02-one-fake-for-the-saas-flows.md` - **wave 2**,
  unblocked (its blocker, ticket 01, is done). Test-side: one fake
  `tailscaleAPI` routing canned JSON through production parsers, replacing the
  three hand-rolled httptest servers. Feature complete after this.

**form-fields-by-key** (candidate 06, no ADR):
- `form-fields-by-key/issues/02-form-submit-rejection-message.md` - **wave 2**,
  unblocked (its blocker, ticket 01, is done). `internal/ui/form.go` submit
  paths emit `FormSubmitRejectedMsg{Key, Reason}` on both rejection paths.
- `form-fields-by-key/issues/03-drivers-click-by-key.md` - **wave 3**, blocked
  by its 02. Integration drivers switch to clicking `field:<key>` targets and
  fail fast on the rejection message; rewrites the AGENTS.md TUI-form hazard
  note. Touches `integration/ui_test.go`, so the post-merge integration run is
  mandatory for its wave.

Read each feature's `PRD.md` alongside the ticket - the grilling decisions
live there.

Conflict map: label-module and mode-daemon/02 both touch `tailscale.go`-adjacent
ground but different functions (codec fronts vs control-seam reads);
api-adapter/02 and mode-daemon/02 may collide in `tailscale_validate_test.go`.
Suggested merge order: form-keys/02, once-label-module/01,
tailscale-api-adapter/02, tailscale-mode-daemon/02.

## Fan-out recipe (wave-1 proven)

Per ticket, one Agent tool call: `subagent_type: general-purpose`,
`model: opus`, `isolation: worktree`, background. Prompt must include:

- First step: `git checkout -b <feature>-<NN>-<slug>`.
- Absolute paths to ticket, PRD, and governing ADR; note AGENTS.md rules.
- A scope guard naming exactly which functions/files sibling agents own and
  must not be touched. This is what kept wave 1 to one conflict.
- Drive with `mattpocock-skills:tdd`; before finishing run
  `mattpocock-skills:code-review` against main (spec source: the ticket) and
  fix real findings.
- `make test` green and lint clean required. Do NOT run the full integration
  suite (coordinator runs it once post-merge).
- End state: ticket `**Status:** in-review` plus a `## Comments` entry with
  date/branch/summary. **`.scratch/` is now committed on main** (unlike wave
  1), so agents should edit the ticket inside their own worktree and commit
  it on their branch - wave-1 agents were blocked writing to the primary
  checkout by worktree isolation and resorted to workarounds.
- No em dash (use "-"), no co-author lines, never touch CHANGELOG.md.

Coordinator afterwards: integration branch off main, merge in the order
above resolving conflicts, `go vet` + `make test` +
`PATH="$HOME/go/bin:$PATH" make lint`, `docker pull hello-world` as a
registry sanity check, `make integration` once (10-minute budget; wave-1
took 437s), flip tickets to done citing merge hashes, `git worktree remove
--force` the agent worktrees (branches keep the commits), fast-forward main.

## Wave plan summary

- **Wave 2** (now, 4 parallel): once-label-module/01, tailscale-mode-daemon/02,
  tailscale-api-adapter/02, form-fields-by-key/02.
- **Wave 3** (after wave 2 merges, 2 parallel): once-label-module/02,
  form-fields-by-key/03.
- **Wave 4** (after wave 3 merges, 1): tailscale-mode-daemon/03.
- After wave 4, every ticket from architecture-review candidates 02-07 is
  done. Each wave repeats the same recipe: fan out, merge, verify, flip
  tickets, remove worktrees, fast-forward main.

## Decisions that constrain (do not re-argue)

- ADRs 0008-0012 are committed; read the governing one before touching its
  ground. The "multiple VPN providers coming" constraint is recorded in
  0008/0010/0011: single doors, sibling adapters, no provider abstraction
  until a second provider exists.
- Warnings convention is `([]string, error)`. Frontend-specific error
  wording stays per-frontend. `Equal` ignores the label carry.
- In mode-daemon/02, the four SaaS-skip branch points keep calling
  `controlSeamActive` where they are; the ticket text governs the exact
  scope.

## Repo gotchas

- `golangci-lint` is not on the default PATH; bare `make lint` fails with
  "No such file or directory". Use `PATH="$HOME/go/bin:$PATH" make lint`.
- zsh: the user profile's assoc-array shim prints harmless
  `command not found: _encode` noise in Bash calls; quote grep globs
  (`--include="*.go"`).
- Integration suite: 10-minute total budget, one hung test masks the culprit
  (read the goroutine dump, not the alarm); `ImagePull` always contacts the
  registry - IPv6 route flaps to Docker Hub are an environment problem, not
  flakiness.
- Six older `.scratch/sidecar-default-mode` tickets sit at `in-review` from
  a pre-review wave; not this effort's scope.
- Never use the em dash. Never add co-author lines. `gh-axi` not `gh`;
  `chrome-devtools-axi` for browser work. Run `deja "<query>"` before
  debugging or re-implementing.
- Ponytail mode (lazy/YAGNI, SessionStart hook) shaped all designs; keep
  preferring the laziest thing that works.

## Suggested opening move

Confirm `main` is at `0da2dc4` and clean, then launch all four agents in a
single message so they run concurrently.
