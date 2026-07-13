# Research: updating the tailnet ACL via the Tailscale REST API

Status: done
Labels: wayfinder:research
Blocked by: (none)

## Question

Can Once overcome the docktail ACL setup burden by programmatically updating
the tailnet policy file through the Tailscale REST API? Specifically:

- The policy endpoints: GET/POST /api/v2/tailnet/{t}/acl - exact semantics,
  the If-Match / etag conflict mechanism, and the validate-only mode
- Required OAuth scope for policy writes, and whether granting it to Once's
  OAuth client is reasonable (blast radius: that scope can rewrite the whole
  tailnet policy)
- Format safety: tailnet policies are HuJSON with comments; does the API
  round-trip comments and formatting, or does a programmatic write clobber a
  hand-edited file? Is a targeted section merge (tagOwners,
  autoApprovers.services, grants svc:, nodeAttrs) feasible without touching
  the rest?
- Failure modes: what happens on a validation error, concurrent edit, or a
  policy managed by GitOps (e.g. gitops-acl action) where API writes fight
  the pipeline
- Resulting UX sketch: could 'once tailscale enable' offer to apply the four
  needed sections automatically (with user confirmation and a dry-run diff),
  falling back to printing the snippet?

Context: the go/no-go decision (issue 06) was deferred pending this - the
user's question: "Can ACL burden be overcome if the tailscale REST API is
used to update ACL? Research how this impacts the solution." The provisional
ACL-UX preference is api-write. Primary sources: tailscale.com/api docs,
tailscale.com/kb (policy syntax, OAuth scopes), GitHub discussions/issues.

## Resolution

Verdict: yes, api-write is viable and reasonably safe. The API round-trips
HuJSON verbatim (comments preserved), supports ETag-based optimistic locking,
and has a validate-only endpoint. The write is a full-file replace, so the
safety burden is client-side: Once must GET the raw HuJSON, patch only the
four sections with github.com/tailscale/hujson (Tailscale's own AST library,
built for exactly this), show a diff, and POST back with If-Match. Keep the
print-the-snippet path as the fallback and as the recommended path for
GitOps-managed tailnets.

### Endpoint semantics

- GET /api/v2/tailnet/{t}/acl returns the policy file as stored, HuJSON by
  default (Accept: application/json for stripped JSON), plus an ETag response
  header. Optional ?details=1 returns base64 HuJSON with parse errors and
  warnings.
- POST /api/v2/tailnet/{t}/acl fully replaces the policy file (no server-side
  partial merge). Accepts JSON or HuJSON. Optional If-Match header: pass the
  ETag from GET to fail with 412 on a concurrent edit, or the special value
  "ts-default" to write only if the tailnet still has the untouched default
  policy (useful for first-run setup). Returns 400 and applies nothing if the
  policy is invalid or its embedded ACL tests fail.
- POST /api/v2/tailnet/{t}/acl/validate is the dry-run: send the hypothetical
  policy (object mode) or just tests against the current policy (array mode);
  neither modifies the tailnet. It returns 200 even on failure, so the
  response body must be checked. A past bug where it rejected HuJSON comments
  (tailscale/tailscale#9910) is closed; POST /acl also validates on apply, so
  validate is belt-and-suspenders.
- POST /api/v2/tailnet/{t}/acl/preview exists for "which rules match this
  user/ipport" previews; not needed for our flow.

### OAuth scope and blast radius

- Reading and validating needs scope policy_file:read; writing needs
  policy_file (read, validate, and modify). There is no narrower "only these
  sections" scope: policy_file can rewrite the entire tailnet policy,
  including locking every admin out via a bad grants block.
- Scopes are fixed at OAuth client creation. Mitigation that keeps the blast
  radius acceptable: do not add policy_file to Once's long-lived runtime
  client (the one that mints auth keys). Instead, 'once tailscale enable'
  asks for a separate, optionally throwaway OAuth client (or token) with
  policy_file scope, used once during setup and never stored. Access tokens
  expire after one hour, which suits a one-shot setup step. If the user
  declines, fall back to the snippet.

### Format safety and targeted merge

- The API speaks HuJSON end to end: GET returns the stored file verbatim,
  comments and trailing commas included, and a HuJSON POST is stored as
  sent. Nothing is clobbered by the transport itself.
- Clobbering only happens if the client re-marshals a Go struct. The fix is
  github.com/tailscale/hujson: Parse the fetched bytes into an AST that
  keeps comments and whitespace, use Find (RFC 6901 pointers) and Patch
  (RFC 6902, HuJSON-aware) to add or extend only tagOwners,
  autoApprovers.services, grants (append one grant with the svc: dst), and
  nodeAttrs (append one entry), then Pack and POST. Untouched regions are
  byte-identical, so a hand-edited file survives. Caveat: Patch mutates in
  place on failure, so Clone first.

### Failure modes

- Validation error: POST returns 400 with details, nothing applied. Surface
  the message and print the snippet for manual application.
- Concurrent edit: with If-Match set, POST returns 412. Re-fetch, re-patch,
  re-diff, re-confirm. Never POST without If-Match.
- GitOps-managed policy: the gitops-acl action is one-way, git is the source
  of truth, and the next sync silently overwrites any API or admin-console
  change. There is no API signal that a tailnet is GitOps-managed, so this
  is undetectable; the confirm prompt must warn "if you manage your policy
  via GitOps, answer no and commit the snippet to your repo instead". (The
  admin console's editor lock only stops console edits by humans.)

### UX sketch: 'once tailscale enable'

1. Configure tailnet + runtime OAuth client as today. Then: "Once needs 4
   policy file sections (tagOwners, autoApprovers.services, grants,
   nodeAttrs). Apply them via the Tailscale API? [y/N]". No -> print the
   HuJSON snippet with instructions, done.
2. Yes -> prompt for a policy_file-scoped OAuth client id/secret (with a
   one-line note that this credential can rewrite the whole policy, is used
   once, and is not stored), or reuse the runtime client if it already has
   the scope (probe: GET /acl, 403 means no).
3. GET /acl (HuJSON), keep ETag. hujson-Patch in the four sections,
   idempotently: skip any section whose entries already exist, so re-running
   enable is a no-op.
4. Show a unified diff old vs new and the GitOps warning; confirm. A
   --dry-run flag stops here after printing the diff.
5. POST /acl/validate with the new file; on failure show errors and fall
   back to the snippet.
6. POST /acl with If-Match: "<etag>". 412 -> loop from step 3 (bounded
   retries). 400 -> show error, print snippet. 200 -> "Policy updated".
7. Failure of any step degrades to printing the snippet; api-write is an
   accelerator, never the only path.

### Sources

- https://tailscale.com/api (Policy file endpoints; historical mirror
  github.com/tailscale/tailscale api.md, v1.62.0)
- https://tailscale.com/kb/1337/acl-syntax (policy file syntax: tagOwners,
  autoApprovers, grants, nodeAttrs; HuJSON)
- https://tailscale.com/docs/features/oauth-clients (policy_file and
  policy_file:read scopes; scopes fixed at client creation; 1h tokens)
- https://github.com/tailscale/hujson (comment/format-preserving Patch,
  Find, Format, Pack)
- https://tailscale.com/kb/1204/gitops-acls and
  https://tailscale.com/kb/1306/gitops-acls-github (gitops-acl action
  overwrites console/API edits on next sync; editor lock)
- https://github.com/tailscale/tailscale/issues/9910 (validate endpoint
  HuJSON comment bug, closed)
- https://pkg.go.dev/tailscale.com/client/tailscale (SetACL WithETag /
  avoidCollisions, 412 on mismatch)

## Comments

2026-07-13 follow-up (from lavish review): can the API also create the OAuth
clients themselves? Yes - since May 2025 OAuth clients can be managed via the
API (tailscale/tailscale#9632 closed: POST /api/v2/tailnet/{t}/keys now
creates OAuth clients; Terraform tailscale_oauth_client resource exists). The
creating credential needs the oauth_keys scope, which is read/modify of OAuth
credentials - effectively admin-equivalent, since it can mint further
credentials. Implication: Once could ask for ONE bootstrap credential at
enable and mint the runtime + one-shot policy clients itself; the console
step shrinks to creating that single bootstrap client (chicken-and-egg makes
zero console steps impossible). Security posture: bootstrap (oauth_keys) and
policy (policy_file) credentials are setup-time-only - use once, never
store; only the minimal-scope runtime client persists. To pin down in the
spec: whether a created client's scopes must be a subset of the creator's.
