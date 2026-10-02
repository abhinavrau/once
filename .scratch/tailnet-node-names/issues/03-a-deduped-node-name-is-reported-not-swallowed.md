# 03 - A deduped node name is reported, not swallowed

**What to build:** When Once asks the tailnet for a Host and gets a different
one back, it says so, and it routes on the name it actually got. Today the
substitution is silent, and the route falls back to the requested name
whenever the node is not there to be asked.

**Blocked by:** 01 - the Services sweep on topology leave should land first, so a services->nodes switch does not leave Once's own leftover `svc:` names refusing every deploy.

**Status:** done

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

- [x] A Deploy whose node joins under a name other than the requested Host
      surfaces that fact to the user, naming both the requested and the
      assigned name.
- [x] The message points at the likely cause, so the reader is not left to
      discover that a Tailscale Service can hold a Magic DNS name.
- [x] Whether a mismatch fails the Deploy or warns and continues is decided at
      triage and applied consistently across the CLI and the TUI.
- [x] The same reporting covers `once-admin`, which joins through the same path
      and hit the same problem.
- [x] Unit coverage for the comparison: an exact match is silent, a `-1` style
      substitution is reported, and a Host that is a prefix of another node's
      name is not a false positive.
- [x] A route registered on a composed Host, because the node reported no FQDN
      at the time, is re-registered on the node's real FQDN once it is
      knowable, with no manual redeploy.
- [ ] Registering a nodes route no longer waits on the node, so a Roll costs
      what it did before the FQDN read was introduced. The round-trip
      integration test returns to its former runtime.
- [x] The existing nodes integration tests still pass, including the Roll paths
      (`TestTailscaleEnableBootsNodesTopology`,
      `TestTailscaleRoundTripsBetweenNodesAndServices`) that the first attempt
      at requiring a live node broke.

## Comments

Implemented (commit "A deduped node name fails the deploy, naming both
names"). Not live-verified on SaaS: the integration harness runs on the
Control Seam, which skips the Services read and never dedupes, so the
rename refusal and the scope probe are unit-covered only.

- Enable: `validateNodesCredentials` also lists Services (Docktail's request,
  `GET /tailnet/-/services`); a 403 names `services:read` and says to create
  a new OAuth client, since scopes are fixed at creation. The scope list,
  TUI row and setup prose already carried it (ticket 01); ADR-0006's
  amendment now does too.
- Preflight: `validateNodesHostAgainstTailnet` returns `servicesScopeMissing`
  on a 403; `validateHost` logs the degradation and proceeds. "Remembered for
  that deploy" is done by re-reading `svc:<host>` at mismatch time instead of
  carrying state, which also covers once-admin (it never runs validateHost).
- First vs re-registration: not derived from the Proxy's route table
  (kamal-proxy has no machine-readable list). `registerNodesProxyRoute` takes
  `joined`, the Host a node that already joined answers on. An app reads it
  from TSDProxy once, before its container is replaced - TSDProxy drops the
  proxy mid-swap, so that is a Roll's only chance to see it, and the Roll
  then pays no wait at all. Empty means the node joins now: wait
  (`nodeJoinWaitTimeout`), compare the leading label, refuse a mismatch.
  The sweep and once-admin's Boot/re-issue read once (no wait), composed
  fallback; once-admin's create is a join.
- A re-registration whose node holds a deduped name only warns and routes on
  it, so a pre-existing `-1` node keeps working rather than failing every Roll.
- Accepted: a Roll whose node TSDProxy is not reporting just then counts as a
  join, so it pays the wait and, on a pre-existing `-1` node, fails with the
  rename error (the old container keeps serving). A slow first join still
  composes after the wait, unchecked, until a proxy roll heals it.
- Rejected in review: releasing the renamed device on refusal. The
  hostname filter could also delete a live node on that Roll path. An app's
  leftover is already swept by the next deploy's preflight; the message says
  the node keeps the name until its device is deleted.
- Runtime criterion left open: `TestTailscaleRoundTripsBetweenNodesAndServices`
  ran 69.7s (94.9s before), not ~17.5s. Instrumented, route resolution now
  costs ~6s of it: three joins at one 2s poll each, and a re-issue under 1ms.
  So no FQDN wait expires any more. The remaining gap is outside route
  registration and was not chased here.
  `TestTailscaleEnableBootsNodesTopology` passed (16.5s).

## Agent Brief

**Category:** bug
**Summary:** Refuse a deploy whose Host is already held on the tailnet (device or Service), hard-error when the node still joins under a deduped name, and heal composed routes - so a rename is never silent and never permanent.

**Triage decisions (maintainer, 2026-08-31):**

1. The Nodes credential now also requires the Tailscale **services read** scope,
   so the preflight can see Service holders. The enable-time probe proves it,
   the same way it proves `auth_keys` and `devices:core` today.
2. A deploy whose preflight cannot read Services (a grandfathered credential
   without the scope) **degrades to the devices-only check and proceeds** -
   it does not fail the deploy. The degradation is remembered for that deploy.
3. A post-registration mismatch between the requested Host and the leading
   label of the FQDN the node actually joined under **fails the deploy**,
   naming both names. When the Services check was degraded, the message says
   the credential lacks the services scope and a Tailscale Service may be
   holding the name, with re-mint instructions. When Services were readable,
   the message names the remaining causes (a name claimed between check and
   join, or a swept device whose name Tailscale had not freed yet).
4. The logs scope is **excluded**: no code path consumes it, docktail's docs
   require no logs scope, and ADR-0006 is a minimal-scope decision. Raise
   separately if a concrete read appears.

**Current behavior:**
The nodes preflight (`validateHost` and its live check) refuses a Host held by
an existing tailnet *device*, exempting the app's own node, but cannot see
Tailscale Services - a leftover `svc:` name sails past and Magic DNS silently
hands the node a `-1` variant. `NodeRouteHost` prefers the FQDN the node
reports but never compares it to the requested name, waits a fixed
`nodeFQDNWaitTimeout` on every registration (so every Roll pays it before
falling back), and falls back to composing name + Magic DNS suffix with only a
`slog.Warn`. A composed route is never corrected afterwards; the post-proxy-roll
sweep (`reregisterNonPublicRoutes`) re-issues routes but pays the wait again
per app.

**Desired behavior:**
- Enable: probing a credential without the services read scope fails with a
  guided message (create a new OAuth client with the listed scopes - scopes are
  fixed at client creation - then re-run enable). The advertised scope list
  (`TopologyFacts.OAuthScopes`, the TUI's OAuth setup steps, ADR-0006/0015
  scope text) includes the services scope.
- Deploy preflight: the live host check reads Tailscale Services as well as
  devices. A Host held by a Service is refused before anything joins, naming
  the `svc:` holder and how to remove it. A Services read that fails for lack
  of scope degrades to the devices-only check and lets the deploy proceed.
- After registration: the resolved FQDN's leading label is compared to the
  requested Host. Exact match is silent. A mismatch fails the deploy with both
  names and the cause message per decision 3 above. `once-admin` registers
  through the same path and gets the same comparison.
- The FQDN wait is split, as the existing `ponytail:` comment on
  `nodeFQDNWaitTimeout` anticipates: a **first registration** (the app has no
  existing route on the Proxy) waits for the node's FQDN, because that is the
  only moment a dedupe can be detected and errored. A **re-registration** (the
  route already exists - a Roll, a settings change, the post-proxy-roll sweep)
  reads once with no wait and falls back to the composed Host, since TSDProxy
  routinely drops the proxy from its API while the container is replaced.
- A route registered on a composed Host heals: the post-proxy-roll sweep
  re-resolves it onto the node's real FQDN once the node reports one, with no
  manual redeploy.

**Key interfaces:**
- `nodesTopology.facts().OAuthScopes` - add the services read scope string.
- `validateNodesCredentials` - add a Services-list probe alongside the
  existing key-mint and device-list probes, with a 403 mapped to a guided
  missing-scope error.
- `validateNodesHostAgainstTailnet` - additionally consult Services via
  `tailscaleAPI`; a scope failure downgrades to the existing device check and
  reports the degradation to its caller rather than swallowing it.
- `nodeRouteHost` / `Tailscale.NodeRouteHost` - accept (or derive) whether
  this is a first registration; compare requested name against the returned
  FQDN's leading label and return the mismatch error. The comparison must not
  false-positive on a Host that is a prefix of another node's name.
- `registerNodesProxyRoute` - shared by apps and once-admin; the mismatch
  error and the healing behavior ride through it unchanged for both callers.
- The Services read must sit behind the control seam (headscale has no
  Services API) with httptest unit coverage byte-compatible with the real
  Tailscale API call, per the testing notes in AGENTS.md.

**Acceptance criteria:**
- [x] Enable with a credential lacking the services read scope fails the probe
      with a message naming the scope and the fix; enable with the full scope
      set succeeds.
- [x] A deploy whose Host is held by a Tailscale Service is refused before the
      node joins, naming the service.
- [x] The same deploy under a credential that cannot read Services proceeds on
      the devices-only check.
- [x] A node that joins under a name other than the requested Host fails the
      deploy, naming both names; the degraded-credential variant of the message
      names the missing services scope as the likely reason a holder went
      unseen.
- [x] An exact match is silent; a Host that is a prefix of another node's name
      is not a false positive. Unit-covered.
- [x] `once-admin` gets the same comparison through the shared registration
      path.
- [x] A route registered on a composed Host is re-registered on the node's
      real FQDN by the post-proxy-roll sweep, with no manual redeploy.
- [ ] A re-registration pays no FQDN wait: the nodes/services round-trip
      integration test returns to its former (~17.5s) runtime.
- [x] Existing nodes integration tests pass, including
      `TestTailscaleEnableBootsNodesTopology` and
      `TestTailscaleRoundTripsBetweenNodesAndServices`.
- [x] Services-read logic is unit-tested via httptest with the request shape
      byte-compatible with the real API; the control seam path skips it.

**Out of scope:**
- The logs scope (decision 4).
- Any change to the Services topology's own preflight (`ShadowWarnings`
  already covers device shadowing there).
- Enumerating holders beyond devices and Services, or a periodic healing
  loop beyond the existing post-proxy-roll sweep.
- Requiring services **write** - the nodes credential needs read only.
