# 03 - Validate outcomes: proceed, warn, refuse, stale

**What to build:** Phase three, the "Validate Policy & Install" step, in all four
end states. A missing section blocks or warns according to what it breaks
(ADR-0017), so this is where the button proves it is willing to refuse.
Validation that cannot refuse is decoration.

**Blocked by:** 02.

**Status:** ready-for-agent

- [ ] Nothing to add: the prototype says so and goes straight to install, showing no diff screen.
- [ ] Missing `funnel` nodeAttr: warns, names that only Tailscale Funnel is affected, and proceeds.
- [ ] Missing 443 grant or `autoApprovers.services`: refuses, naming the section and what it breaks.
- [ ] Tailnet Policy changed since the Merged Policy was generated: reports the change instead of proceeding.
- [ ] Every outcome is reachable from the scenario switcher without a rebuild.
