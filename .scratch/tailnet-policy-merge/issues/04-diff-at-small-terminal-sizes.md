# 04 - Diff readability at small terminal sizes

**What to build:** The Merged Policy diff, usable on a small terminal. The
Tailscale enable form already overflows under about 46 rows, and a policy diff
is far taller than the form, so this is the highest-risk visual question in the
set.

**Blocked by:** 02.

**Status:** ready-for-human

- [x] The diff is usable at 24 rows against a fixture policy of at least 200 lines.
- [x] Nothing is unreachable: content off screen can either be scrolled to, or is explicitly summarised.
- [x] The copy control stays reachable at every size, so a user who cannot read the whole diff can still paste the document.
- [x] Behaviour is checked at 24, 46 and 60 rows.

## Comments

Verdict: usable at 24 rows. The screen's fixed chrome is 15 rows (bordered
copy + Continue buttons are 3 rows each), leaving a 9-row viewport at 24 -
enough with the summary line, auto-scroll to the first addition, and the
scrollbar's green overview-ruler markers. TestDiffScreenFitsSmallTerminals
renders at 24/46/60 and asserts exact-height fit, width <= 80, and that the
copy control and scroll hint are present.

Real problem found and fixed: hujson formats a whole grant onto one ~90-col
line, and the viewport truncated it - the added line's src was unreadable at
any height. Added lines now soft-wrap with a hanging indent (wrapLine).

VHS recordings at all three sizes: recordings/diff-{24,46,60}rows.gif, final
frames as .png, re-runnable .tape files beside them (PROTO_SIZE env pins the
layout size).

Out of scope but observed: the credential screen is 38 rows tall, so it
overflows the 80x24 floor badly - recorded as a finding for ticket 05; the
real form needs to scroll or collapse its steps at small heights.
