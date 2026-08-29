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

**Status:** done

- [x] `GET /tailnet/-/acl` via the Runtime Credential, capturing the ETag for the stale check (ticket 07).
- [x] Merge is topology-aware: `autoApprovers.services` only under Services (ADR-0015); Nodes merges `grants` and `nodeAttrs` only.
- [x] Sections already present are never duplicated or rewritten; comments survive.
- [x] The reformatting contradiction from the ADR-0017 amendment is resolved: either additions insert without reformatting untouched lines, or the diff honestly shows the reformat. Decide, and record the decision in this ticket.
- [x] The 443 grant presence check is an ACL test via `POST /tailnet/-/acl/validate`, not text matching; `nodeAttrs`/`autoApprovers` check the parsed document (ADR-0017).
- [x] Missing/revoked `policy_file:read` degrades to the static `PolicySnippet()` path and says why.
- [x] SaaS-only API calls are covered by httptest unit tests, byte-compatible request shapes.

## Comments

**Decision - reformatting contradiction:** additions insert without
reformatting untouched lines. The prototype's `hujson.Patch` + `Format`
repaints the whole document in hujson's house style, so the paste payload
would not be "the user's document plus additions" - and it would produce a
massive VCS diff for a GitOps-managed policy, which ADR-0017 explicitly set
out not to clobber. Instead, `policyEditor` edits hujson's syntax tree
directly: untouched lines survive byte-for-byte (asserted in
`assertKeepsEveryLine`), and insertions land on their own lines copying the
document's own indentation unit and trailing-comma style, sniffed from what
is already there. One accepted seam: appending to an array whose last element
has no trailing comma necessarily adds the separating comma to that line -
any merge must.

Built in `internal/docker/tailscale_policy.go` beside `PolicySnippet()` (the
no-scope fallback, unchanged): `reviewTailnetPolicy` reads the policy as
HuJSON (`Accept: application/hujson`, ETag captured into
`PolicyReview.ETag`), checks the 443 grant by ACL test (`POST /acl/validate`
with a test array against the live policy), checks
`nodeAttrs`/`autoApprovers.services` against the parsed document (tolerant:
any entry granting the funnel attr to the tag or `*` counts, an existing
approver key is never overwritten), merges only what is missing, and
validates the merged HuJSON through `POST /acl/validate` before returning
it. A 403 on the read returns `PolicyReview.Fallback` naming the missing
`policy_file:read` scope instead of failing - ticket 07 surfaces it and the
flow proceeds on today's static-snippet behavior. The `tailscaleAPI` seam
gained `CallWithHeaders` (request headers out, response headers back) for
the Accept/Content-Type/ETag mechanics; covered by httptest in
`tailscale_api_test.go`, the flow itself by the fake seam (which now
records headers and supports sequenced replies for the two `/acl/validate`
posts).
