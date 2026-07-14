# 16 - ACL provisioning via API write

**What to build:** During `once tailscale enable`, offer to apply the required tailnet policy sections (tagOwners, autoApprovers.services, svc: grants, funnel nodeAttr) via the Tailscale API so the user never hand-edits HuJSON (user story 5):

- Fetch the current policy, patch only the four sections using Tailscale's HuJSON AST library; the patch is idempotent (already-present entries are skipped) and preserves the rest of the policy byte-for-byte, comments included.
- Show a unified diff of the proposed change plus a GitOps warning (a Once write may be reverted by a policy pipeline) before writing.
- Validate server-side, then write with ETag If-Match; on conflict (concurrent edit), bounded retry with a fresh fetch - never clobber.
- Any decline or failure degrades gracefully to printing the exact copy-paste policy snippet with instructions to apply manually and re-run enable.
- The policy-write credential is separate, `policy_file`-scoped, prompted for at enable time, used once, and never stored.

**Blocked by:** 12 (core swap).

**Status:** done

- [x] Enable offers the API policy write with diff and GitOps warning; decline prints the snippet
- [x] Patch touches only the four sections and preserves everything else byte-for-byte including comments; re-running against an already-patched policy is a no-op
- [x] Write uses ETag If-Match with bounded retry on 412; server-side validation failure falls back to the snippet
- [x] The policy-write credential never appears in any persisted settings or container label
- [x] Policy GET/validate/POST requests asserted byte-compatible in httptest unit tests, covering: comment-preserving patch, idempotent re-run, 412 retry, validation-failure fallback
- [x] Lint and unit tests pass

## Comments

2026-07-13: Implemented. internal/docker/tailscale_policy.go holds the API plumbing (PlanPolicyUpdate/Apply/PolicySnippet, httptest-seamed via the private planPolicyUpdate); the interactive offer lives in `once tailscale enable` (internal/command/tailscale.go, offerPolicyProvisioning) and is skipped on the headscale control seam and when stdin is not a terminal. Decisions worth recording:

- The patch edits the HuJSON AST directly (append-only members/elements that copy the sibling's indentation and trailing-comma style) instead of hujson's RFC 6902 Patch, which inserts with no whitespace and produced an unreadable one-line diff. Untouched regions still round-trip byte-for-byte.
- The grant dst is the tag, not per-svc: names: a tag assigned to a Service selects it in grants (kb/1552), and docktail stamps the tag on every Service definition, so one grant covers apps deployed later.
- The offer runs before the runtime-credential probe (PRD sketched the reverse) because on a fresh tailnet the probe cannot pass until the policy defines the tag.
- Idempotency is presence-based per entry; array entries are matched by semantic equality with exactly what Once would add (the snippet shares the same builders, so a pasted snippet also makes the re-run a no-op). A hand-written broader rule is not recognized - Once appends its own entry alongside (marked ponytail in ensureElement).
- Per issue text, a 412 retry re-fetches/re-patches/re-validates without re-prompting (research 08 had sketched re-confirm); concurrent edits are still never clobbered.
