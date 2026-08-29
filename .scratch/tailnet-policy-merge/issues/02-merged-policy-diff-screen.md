# 02 - Merged Policy diff screen

**What to build:** Phase two. After the Runtime Credential fields, the prototype
shows the Merged Policy: the canned existing Tailnet Policy with Once's missing
sections added, rendered as a colour diff.

The diff exists for comprehension. The full merged document is what the user
copies, because Once never writes the Tailnet Policy and the paste is the
critical path (ADR-0017).

**Blocked by:** 01.

**Status:** ready-for-agent

- [ ] Entering credential values and continuing shows the diff screen.
- [x] Added lines are visually distinct from unchanged context, and nothing appears as removed, since Once only ever adds.
- [x] The copy control copies the whole merged document, not the diff rendering.
- [x] A fixture whose policy already contains one of Once's sections shows it as unchanged, never duplicated.
- [x] Comments in the canned policy survive into the merged document.
- [ ] The selected Tailnet Topology changes which sections appear: Nodes adds fewer than Services.

## Comments

Diff screen built early on user request (branch `prototype/tailnet-policy`).
hujson Patch + Format produce the Merged Policy; LCS line diff renders
additions green in a scrolling viewport with Copy/Continue buttons. A 200+
line "Large GitOps policy" fixture exercises scrolling. Still open here: the
Runtime Credential fields before the diff, and the Tailnet Topology switch
(Nodes vs Services section sets).
