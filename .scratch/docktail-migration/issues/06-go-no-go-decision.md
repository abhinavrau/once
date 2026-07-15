# Decision: go/no-go on replacing TSDProxy with docktail

Status: done
Labels: wayfinder:grilling
Blocked by: 01, 02, 03, 05, 08, 09 (all resolved)

## Question

Given the research findings, do we replace TSDProxy with docktail?

Hard bar (from charting): Tailscale Services must work on the free Personal
plan with a workable service cap (issue 01). Also weigh: sidecar privilege
cost (issue 02), test-seam story (issue 03), and setup burden on the user's
tailnet (issue 05).

Resolve via a /grilling session with the human. The resolution is a locked
decision plus the reasoning, recorded here and gisted on the map. If no-go,
the map ends here and the spec ticket (07) is closed out of scope.

## Comments

2026-07-13 (lavish review session): decision deferred - MORE-EVIDENCE.
The human asked: "Can ACL burden be overcome if the tailscale REST API is
used to update ACL? Research how this impacts the solution." Spawned issue 08
(ACL API write) and issue 09 (headscale: Services without serve/Funnel;
policy API parity) as new blockers. Provisional ACL-UX preference if go:
api-write (Once patches the policy itself, with confirmation).

2026-07-13 (lavish review session, round 2): RESOLVED - GO.
Human's rationale: "ACL burden is removed since we are using tailscale API."
Basis: all six research blockers resolved. Free Personal plan bar cleared
(01); sidecar fully unprivileged (02); headscale seam lost for Services,
fake-the-seams locked in (03, 09); URL discovery deterministic via local
MagicDNS suffix (04); the setup-burden objection (05) dissolved by 08:
policy API round-trips HuJSON safely (hujson patch, ETag If-Match, diff +
confirm, snippet fallback, GitOps caveat), and since May 2025 OAuth clients
themselves can be minted via API (oauth_keys scope, one bootstrap
credential). ACL-UX locked: api-write. Next: assemble the PRD (issue 07).
