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
- [ ] Added lines are visually distinct from unchanged context, and nothing appears as removed, since Once only ever adds.
- [ ] The copy control copies the whole merged document, not the diff rendering.
- [ ] A fixture whose policy already contains one of Once's sections shows it as unchanged, never duplicated.
- [ ] Comments in the canned policy survive into the merged document.
- [ ] The selected Tailnet Topology changes which sections appear: Nodes adds fewer than Services.
