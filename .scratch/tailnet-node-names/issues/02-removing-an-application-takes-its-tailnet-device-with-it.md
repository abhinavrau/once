# 02 - Removing an Application takes its tailnet device with it

**What to build:** Under the Nodes topology, `once remove` on a tailnet
Application deletes the tailnet device that Application owned. The tailnet is
left with no device for an Application that no longer exists.

**Blocked by:** None - can start immediately. Independent of ticket 01, which
covers a different leak (Tailscale Services) behind a different symptom.

**Status:** ready-for-agent

## What is actually wrong

`Application.Destroy` removes App Containers and, optionally, the Application
Data Volume. It never deletes the Application's tailnet device, even though the
Nodes topology is what created it.

Verified live: removing a tailnet Application leaves its device in place.

```
=== devices after remove ===
STILL THERE: zzzprobea | zzzprobea.sphynx-degree.ts.net
```

So every install cycle leaves a device holding the Host, and the next Deploy
has to delete it first. The leftover should not exist at
all. It is an orphan of an Application Once no longer
manages, sitting in the user's tailnet under Once's own tag, and it is the same
class of leak that `nodesTopology.remove` already fixes for a topology teardown
(ADR-0015).

Teardown is not affected: `Namespace.Teardown` reaches `nodesTopology.remove`,
which deletes every tagged device. Only the single-Application path leaks.

## Acceptance criteria

- [ ] Removing a tailnet Application under the Nodes topology deletes its
      tailnet device, narrowed to that Application's Host so no other tagged
      device is touched.
- [ ] The delete is best effort. A missing Runtime Credential, an unreachable
      Tailscale API, or an active Control Seam does not fail the removal.
- [ ] Removing a Local or Public Application, or removing under the Services
      topology, deletes no device.
- [ ] Unit coverage against the fake `tailscaleAPI`: the Host's device is
      deleted, a device with a different Host is left alone, and an API error
      still lets the removal report success.
