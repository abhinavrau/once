# 12: Environment variables are CINCH_*

**What to build:** Every `ONCE_*` variable is `CINCH_*`, with no fallback: runtime, Tailscale, installer, test-only, the `CINCH_TAILSCALE_` prefix match, secretspec, the env injected into the systemd unit and launchd plist, and CI (SPEC.md, Environment variables).

**Blocked by:** 11 (Tailscale identity says cinch)

**Status:** ready-for-agent

- [ ] `git grep ONCE_` finds nothing outside historical records
- [ ] The unit/plist tests assert `CINCH_NO_SELF_UPDATE`
- [ ] `make build`, `make test`, `make lint` pass
