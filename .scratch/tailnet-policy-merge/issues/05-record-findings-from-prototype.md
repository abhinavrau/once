# 05 - Record the findings from prototype

**What to build:** Close the loop. The prototype exists to answer design
questions, not to become production code. Capture what it settled,
and draft the real implementation tickets from what was learned.

**Blocked by:** 03, 04.

**Status:** ready-for-human

- [x] What the prototype settled is written down, as an ADR-0017 amendment or a follow-up doc.
- [x] What it disproved is recorded too, including any decision ADR-0017 states that the prototype contradicted.
- [x] Implementation tickets for the real flow are drafted from the findings.

## Comments

Findings captured as an amendment section in
docs/adr/0017-read-the-tailnet-policy-never-write-it.md: what the prototype
settled (diff usable at 24 rows and how, soft-wrap over truncate, topology-
aware merge, outcome set, chrome decisions) and what it contradicted (the
"not a reformatted replacement" claim - hujson Patch+Format reformats the
whole document; and the credential screen cannot hold 4 steps + form at 24
rows).

Implementation tickets drafted: 06 (merge engine), 07 (enable outcomes),
08 (TUI screens), 09 (ui.Form submit-only layout). The credential-form
overflow folds into the existing
.scratch/tailnet-topology/issues/07-enable-form-overflows-small-terminals.md.

The prototype itself stays on branch prototype/tailnet-policy as the primary
source (per the prototype skill: throwaway branch, never merged to main),
with VHS recordings under recordings/. Cherry-pick the ADR amendment and
these tickets to main when closing the branch.
