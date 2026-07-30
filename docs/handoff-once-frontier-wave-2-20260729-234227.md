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

## Wave 2 frontier - 4 tickets, all blockers done, all independent

| Ticket | Ground it touches |
|---|---|
| `.scratch/once-label-module/issues/01-label-codec-module.md` | new `internal/docker/label.go`, `application_settings.go`. Governing ADR-0008. |
| `.scratch/tailscale-mode-daemon/issues/02-control-seam-single-door.md` | `tailscale.go` control-seam reads, `EnableTailscale` credential suppression, deletes the `command/tailscale.go` and `ui/tailscale_form.go` copies and the `TailscaleControlSeamActive` export. Governing ADR-0010. |
| `.scratch/tailscale-api-adapter/issues/02-one-fake-for-the-saas-flows.md` | test-side: one fake `tailscaleAPI` replacing the three hand-rolled httptest servers in `internal/docker` tests. Governing ADR-0011. |
| `.scratch/form-fields-by-key/issues/02-form-submit-rejection-message.md` | `internal/ui/form.go` submit paths, `FormSubmitRejectedMsg`. |

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

## After wave 2

- Wave 3: `once-label-module/02` (blocked by its 01) - closes the live
  `TailscaleSettings` field-destruction bug.
- Wave 4: `tailscale-mode-daemon/03` (blocked by once-label-module/02) and
  `form-fields-by-key/03` (blocked by its 01+02; rewrites the AGENTS.md
  TUI-form hazard note - integration drivers change, so run the suite for it).
- tailscale-api-adapter is complete after wave 2.

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
