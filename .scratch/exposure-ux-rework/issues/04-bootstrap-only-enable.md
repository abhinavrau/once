# 04 - Bootstrap-only `tailscale enable`

**What to build:** Tailscale setup collapses to one credential (per ADR-0004). `once tailscale enable --client-id ... --client-secret ...` is the complete setup: the flags mean the Bootstrap Credential, direct mode and every runtime-credential input path are deleted, and `--tag` defaults to `tag:once` (still overridable). Once mints the Runtime Credential (stored) and a one-shot Policy Credential, shows the planned policy changes as a diff with the GitOps warning, and applies only on consent - `--yes` accepts, `--no-policy-write` declines and prints the HuJSON snippet without ever minting a Policy Credential. The Policy Credential is deleted immediately after a successful write. Non-TTY runs that reach an undecided branch fail fast with a clear message instead of hanging. Mis-scoped credentials produce specific, actionable probe errors.

**Blocked by:** None - can start immediately.

**Status:** ready-for-agent

- [ ] Enable succeeds with exactly `--client-id` and `--client-secret`; tag defaults to `tag:once`
- [ ] All direct-mode flags and paths are gone; the Bootstrap Credential is never stored
- [ ] Policy diff is shown before any write; `--yes` applies, `--no-policy-write` prints the snippet and mints no Policy Credential
- [ ] Policy Credential is deleted after the write; only the Runtime Credential persists
- [ ] Non-TTY without a consent flag fails fast with a clear message
- [ ] Mis-scoped credential probe failures name the missing scope or tag
- [ ] httptest unit tests cover minting, policy plan/write, and client deletion, byte-compatible with the real API
