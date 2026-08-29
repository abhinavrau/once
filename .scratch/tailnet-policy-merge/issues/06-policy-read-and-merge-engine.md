# 06 - Policy read and topology-aware merge engine

**What to build:** The production merge behind `once tailscale enable`
(ADR-0017): read the Tailnet Policy with the Runtime Credential
(`policy_file:read`), compute the Merged Policy, and report which sections
were added. Home: `internal/docker/tailscale_policy.go`, beside
`PolicySnippet()` (which stays as the no-scope fallback).

Prototype reference: `cmd/prototype-tailnet-policy/merge.go` on branch
`prototype/tailnet-policy` (mergePolicy, diffLines, the hasEntry/hasApprovers
checks and their tests).

**Blocked by:** None.

**Status:** ready-for-agent

- [ ] `GET /tailnet/-/acl` via the Runtime Credential, capturing the ETag for the stale check (ticket 07).
- [ ] Merge is topology-aware: `autoApprovers.services` only under Services (ADR-0015); Nodes merges `grants` and `nodeAttrs` only.
- [ ] Sections already present are never duplicated or rewritten; comments survive.
- [ ] The reformatting contradiction from the ADR-0017 amendment is resolved: either additions insert without reformatting untouched lines, or the diff honestly shows the reformat. Decide, and record the decision in this ticket.
- [ ] The 443 grant presence check is an ACL test via `POST /tailnet/-/acl/validate`, not text matching; `nodeAttrs`/`autoApprovers` check the parsed document (ADR-0017).
- [ ] Missing/revoked `policy_file:read` degrades to the static `PolicySnippet()` path and says why.
- [ ] SaaS-only API calls are covered by httptest unit tests, byte-compatible request shapes.
