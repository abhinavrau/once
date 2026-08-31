# 03 - A deduped node name is reported, not swallowed

**What to build:** When Once asks the tailnet for a Host and gets a different
one back, it says so, and it routes on the name it actually got. Today the
substitution is silent, and the route falls back to the requested name
whenever the node is not there to be asked.

**Blocked by:** None - can start immediately.

**Status:** needs-triage

## Why

Magic DNS gives a node the next free variant when the requested name is taken:
ask for `campfire`, get `campfire-1`. That suffix is permanent for that node.
Once reads the assigned FQDN rather than composing one wherever it can, but it
never compares the name it received against the name it asked for. The rename
is invisible.

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

## The route is only right when the node happens to be up

Reading the assigned FQDN is not unconditional, and this is the half of the
problem that is still open in the code rather than only in the reporting.

`NodeRouteHost` prefers the FQDN the node reports, but falls back to composing
the requested name plus the Magic DNS suffix when the node reports nothing
within a short wait, and it logs that fallback rather than failing the Deploy.
The fallback exists because requiring the node to be live broke every Roll:
TSDProxy drops an app's proxy from its API entirely while its container is
being replaced, so a Funnel toggle or a settings change waited out the full
timeout and then failed a Deploy whose route was already correct and unchanged.

That fallback leaves two costs, both of which this ticket should close:

- **A renamed node routes on the wrong Host until something redeploys it.**
  The composed Host is exactly the pre-existing bug this feature set to fix.
  It is no worse than what Once did before it could read assigned names at
  all, so it is a fair fallback, but nothing corrects it afterwards. The route
  should heal once the FQDN becomes knowable, rather than being resolved once
  at registration time and left. The post-proxy-roll sweep that already
  re-issues app and `once-admin` routes is the natural home for that.
- **Every Roll pays the wait before falling back.** The expiry is the routine
  case on a Roll, not the exceptional one, so the wait buys nothing there and
  costs the user real time: the nodes/services round-trip integration test went
  from 17.5s to 94.9s on this alone. Once the route can heal, registration can
  read once and move on instead of waiting at all.

The wait is marked with a `ponytail:` comment naming this as the upgrade path.

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
- [ ] A route registered on a composed Host, because the node reported no FQDN
      at the time, is re-registered on the node's real FQDN once it is
      knowable, with no manual redeploy.
- [ ] Registering a nodes route no longer waits on the node, so a Roll costs
      what it did before the FQDN read was introduced. The round-trip
      integration test returns to its former runtime.
- [ ] The existing nodes integration tests still pass, including the Roll paths
      (`TestTailscaleEnableBootsNodesTopology`,
      `TestTailscaleRoundTripsBetweenNodesAndServices`) that the first attempt
      at requiring a live node broke.
