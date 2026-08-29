# 03 - Validate outcomes: proceed, warn, refuse, stale

**What to build:** Phase three, the "Validate Policy & Install" step, in all four
end states. A missing section blocks or warns according to what it breaks
(ADR-0017), so this is where the button proves it is willing to refuse.
Validation that cannot refuse is decoration.

**Blocked by:** 02.

**Status:** ready-for-agent

- [x] Nothing to add: the prototype says so and goes straight to install, showing no diff screen.
- [x] Missing `funnel` nodeAttr: warns, names that only Tailscale Funnel is affected, and proceeds.
- [x] Missing 443 grant or `autoApprovers.services`: refuses, naming the section and what it breaks.
- [x] Tailnet Policy changed since the Merged Policy was generated: reports the change instead of proceeding.
- [x] Every outcome is reachable from the scenario switcher without a rebuild.

## Comments

Built on branch `prototype/tailnet-policy`. Outcomes are canned per fixture
via a scenario.outcome field (no API to validate against): Fresh/Existing
proceed, Everything-present skips the diff entirely and installs, Missing
funnel warns and proceeds, Missing 443 grant refuses naming both broken
behaviours, Large GitOps reports stale. A test asserts all four outcomes
stay reachable from the switcher.
