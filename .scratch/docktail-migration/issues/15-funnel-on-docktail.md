# 15 - Funnel on Docktail

**What to build:** `once tailscale funnel enable <app> --duration` keeps its exact current user-facing semantics on the new mechanism: bounded duration (default 10m, max 24h), background daemon required and responsible for automatic teardown at expiry, and activation verified before success is reported. The mechanism becomes `docktail.funnel.*` labels applied by the same atomic container recreation (Roll), and the activation check moves from the tsdproxy API to the local daemon's funnel status (ticket 10's interface). Funnel disable and the TUI funnel form keep working identically.

**Blocked by:** 12 (core swap).

**Status:** ready-for-agent

- [ ] Funnel enable applies `docktail.funnel.*` labels via Roll; disable strips them
- [ ] Duration validation, default, and max are unchanged; background daemon still tears down at expiry
- [ ] Success is reported only after activation is confirmed via the local daemon's funnel status
- [ ] Activation-wait and expiry paths covered by unit tests through the daemon fake; integration test covers label lifecycle
- [ ] Funnel TUI form unchanged in fields (or its integration driver updated in the same change)
- [ ] Lint, unit, and integration tests pass
