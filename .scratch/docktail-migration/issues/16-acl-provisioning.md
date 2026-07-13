# 16 - ACL provisioning via API write

**What to build:** During `once tailscale enable`, offer to apply the required tailnet policy sections (tagOwners, autoApprovers.services, svc: grants, funnel nodeAttr) via the Tailscale API so the user never hand-edits HuJSON (user story 5):

- Fetch the current policy, patch only the four sections using Tailscale's HuJSON AST library; the patch is idempotent (already-present entries are skipped) and preserves the rest of the policy byte-for-byte, comments included.
- Show a unified diff of the proposed change plus a GitOps warning (a Once write may be reverted by a policy pipeline) before writing.
- Validate server-side, then write with ETag If-Match; on conflict (concurrent edit), bounded retry with a fresh fetch - never clobber.
- Any decline or failure degrades gracefully to printing the exact copy-paste policy snippet with instructions to apply manually and re-run enable.
- The policy-write credential is separate, `policy_file`-scoped, prompted for at enable time, used once, and never stored.

**Blocked by:** 12 (core swap).

**Status:** ready-for-agent

- [ ] Enable offers the API policy write with diff and GitOps warning; decline prints the snippet
- [ ] Patch touches only the four sections and preserves everything else byte-for-byte including comments; re-running against an already-patched policy is a no-op
- [ ] Write uses ETag If-Match with bounded retry on 412; server-side validation failure falls back to the snippet
- [ ] The policy-write credential never appears in any persisted settings or container label
- [ ] Policy GET/validate/POST requests asserted byte-compatible in httptest unit tests, covering: comment-preserving patch, idempotent re-run, 412 retry, validation-failure fallback
- [ ] Lint and unit tests pass
