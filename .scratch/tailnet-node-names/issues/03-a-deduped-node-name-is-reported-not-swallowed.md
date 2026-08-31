# 03 - A deduped node name is reported, not swallowed

**What to build:** When Once asks the tailnet for a Host and gets a different
one back, it says so. Today the substitution is silent, and the user finds out
by noticing an odd URL days later.

**Blocked by:** None - can start immediately.

**Status:** needs-triage

## Why

Magic DNS gives a node the next free variant when the requested name is taken:
ask for `campfire`, get `campfire-1`. That suffix is permanent for that node.
Once handles it correctly for routing - `waitForNodeFQDN` deliberately reads
the assigned FQDN rather than composing one - but it never compares the name it
received against the name it asked for. The rename is invisible.

`validateHost` does guard the obvious case: a tailnet *device* already holding
the name is refused before the Deploy. It cannot see every holder. A Tailscale
Service holds a Magic DNS name and is not a device, so a leftover `svc:campfire`
sails past the check and the node quietly becomes `campfire-1`. Ticket 01 fixes
that particular leak. This ticket makes the class of failure visible, whatever
holds the name next.

The diagnostic cost of the silence is the point. Working out why `campfire-1`
existed took a full session: the tailnet device list showed one device with
`hostname: campfire`, nothing held `campfire`, a teardown did not clear it, and
a wipe of the TSDProxy state directory did not either. A single line at Deploy
time would have replaced all of it.

Two shapes to consider at triage, not necessarily both:

- **After registration:** compare the assigned FQDN's leading label against the
  requested Host. On a mismatch, tell the user which name they got, that the
  requested one is held by something Once cannot see, and where to look.
  Catches every cause, needs no new API scope, and cannot produce a false
  negative. It is after the fact, though: the node already carries the suffix.
- **Before registration:** widen the pre-flight beyond devices. This needs the
  services scope to read Service definitions, which the Nodes credential does
  not request today, and it still cannot enumerate every possible holder. It
  can refuse rather than report, which is the better outcome when it fires.

The first is cheap and complete; the second is the nicer experience where it
applies. Worth deciding whether the mismatch should fail the Deploy or warn and
continue - a `-1` node does work, it is just not the name the user chose.

## Acceptance criteria

- [ ] A Deploy whose node joins under a name other than the requested Host
      surfaces that fact to the user, naming both the requested and the
      assigned name.
- [ ] The message points at the likely cause, so the reader is not left to
      discover that a Tailscale Service can hold a Magic DNS name.
- [ ] Whether a mismatch fails the Deploy or warns and continues is decided at
      triage and applied consistently across the CLI and the TUI.
- [ ] The same reporting covers `once-admin`, which joins through the same path
      and hit the same problem.
- [ ] Unit coverage for the comparison: an exact match is silent, a `-1` style
      substitution is reported, and a Host that is a prefix of another node's
      name is not a false positive.
