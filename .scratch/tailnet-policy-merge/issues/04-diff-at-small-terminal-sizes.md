# 04 - Diff readability at small terminal sizes

**What to build:** The Merged Policy diff, usable on a small terminal. The
Tailscale enable form already overflows under about 46 rows, and a policy diff
is far taller than the form, so this is the highest-risk visual question in the
set.

**Blocked by:** 02.

**Status:** ready-for-agent

- [ ] The diff is usable at 24 rows against a fixture policy of at least 200 lines.
- [ ] Nothing is unreachable: content off screen can either be scrolled to, or is explicitly summarised.
- [ ] The copy control stays reachable at every size, so a user who cannot read the whole diff can still paste the document.
- [ ] Behaviour is checked at 24, 46 and 60 rows.
