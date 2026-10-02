# 01 - Leaving the Services topology takes its Tailscale Services with it

**What to build:** When Once leaves the Services topology, by teardown or by a
switch to Nodes, the Tailscale Services it advertised stop existing. The Magic
DNS names they held are free for whatever Once does next.

**Blocked by:** None - can start immediately.

**Status:** ready-for-agent

## What is actually wrong

`servicesTopology.remove` removes the Docktail container, the sidecar, and the
socket volume. `destroy` adds the sidecar's state volume. Neither deletes the
Tailscale Services that Docktail advertised. The Service definitions outlive
the install that made them.

The code comment says Services "has no per-app device to best-effort delete
when it is left, unlike nodes". That is true of devices, and it is why the leak
was not noticed: a Service is not a device, so the device sweep never sees it,
and no other path deletes it.

A Tailscale Service holds a Magic DNS name. So a leftover `svc:campfire` keeps
the name `campfire` occupied for ever, in a tailnet where no device by that
name exists and nothing in the Tailscale device list explains why.

Under the Nodes topology, TSDProxy then asks for `campfire`, Magic DNS refuses
it, and the node joins as `campfire-1`. That suffix is welded to the node
identity for life. It survives a full teardown, a Tailscale clean, and a wipe
of the TSDProxy state directory, because none of those touch the Service that
is actually holding the name.

## Evidence

From the tailnet configuration audit log:

```
2026-08-12T18:38:28 CREATE | SERVICE | svc:campfire
```

Created, never deleted. Five leftover Services were still present when this was
found, from installs long gone:

```
svc:campfire   svc:once-admin   svc:once-campfire   svc:fizzy   svc:whoami
```

Deleting them by hand, then recreating the nodes, is what finally produced
`campfire.<suffix>` and `once-admin.<suffix>` with no suffix. That confirms the
Services were the thing holding the names.

Across the whole 30 day audit retention window, a node named plain `campfire`
or plain `once-admin` was never created once. Every single creation is already
suffixed:

```
2026-08-21T20:34:37 CREATE once-admin-1
2026-08-29T22:41:42 CREATE campfire-1
2026-08-30T00:24:27 CREATE once-admin-1
2026-08-30T00:24:55 CREATE campfire-1
2026-08-30T02:16:46 CREATE campfire-1
2026-08-30T02:16:46 CREATE once-admin-1
```

Control, in the same tailnet and through the same credential: names that never
went through the Services topology register clean, every time, including
straight after a delete of a node holding that exact name.

```
2026-08-30T02:20:59 CREATE campfirexyz
2026-08-30T02:23:31 CREATE campfirexyz    (after the 02:22:58 delete)
2026-08-30T00:24:33 CREATE vaultwarden
```

`vaultwarden` is clean because it was added after the switch to Nodes, so no
`svc:vaultwarden` was ever advertised. `campfire` and `once-admin` were exposed
under Services first. That is the whole difference.

`svc:once-admin` does not appear in the log because its creation predates the
30 day retention. The mechanism is the same, and the symptom is identical.

Two explanations were tested and falsified before this one, both worth not
repeating:

- **Magic DNS retains deleted names.** It does not. A name whose only device is
  deleted is immediately reusable, verified by round-tripping `campfirexyz`.
- **Once's own leftover-device sweep races the replacement's registration.**
  The race is real and reproducible in isolation, but it is not what happens
  here. The `-1` appears from a verified-empty tailnet with wiped state
  directories and no delete in flight.

Note for whoever fixes this: the surviving device's `hostname` field still
reads `campfire` even though its Magic DNS name is `campfire-1`, and that
occupies the base slot too. A third node asking for `campfire` is given
`campfire-2`. So the names stack up rather than settle.

## Acceptance criteria

- [x] Leaving the Services topology deletes every Tailscale Service Once
      advertised under it. This covers `servicesTopology.remove`, `destroy`,
      and a topology switch away from Services.
- [x] The delete is best effort and scoped to Once's own Services. A missing
      services scope, an unreachable Tailscale API, or an active Control Seam
      does not fail the removal, and a Service Once did not create is never
      touched.
- [ ] A Services install torn down and re-enabled under Nodes gives every
      Application its own undeduped Host. Verified against a live tailnet.
- [x] The `services:write` scope requirement is stated where the user supplies
      the Runtime Credential, since removal now needs it and the Nodes
      credential does not carry it today.
- [x] Unit coverage against the fake `tailscaleAPI`: Once's Services are
      deleted, an unrelated Service is left alone, and an API error still lets
      the removal report success.
- [x] Existing installs are not stranded. Once surfaces a leftover Service that
      is holding an Application's Host, rather than silently joining as `-1`.
      A one-line diagnosis beats a deduped name nobody can explain.

## Comments

Implemented (commit "Leaving the Services topology deletes its Tailscale
Services"). Not done: the live-tailnet verification - the integration harness
only drives the headscale Control Seam, which has no Services.

- The topology seam gained `leave`: `remove` plus a best-effort delete of
  every Tailscale Service carrying Once's tag (Docktail creates each with
  `DEFAULT_SERVICE_TAGS`). `Disable`, `destroy`, and an Enable that switches
  topology call it; an Enable that rebuilds within Services only removes, so
  a credential or mode change does not drop and re-create every Service.
  Nodes' `leave` is its `remove`, which already sweeps devices.
- `services:write` is now stated in the Services setup prose as also needed
  for that delete. The Services credential already carries it, and the
  switch and teardown use the stored Services credential, so nothing new is
  required of the Nodes credential to delete.
- Existing installs: the Nodes deploy preflight reads `svc:<host>` and
  refuses a Host a Service holds, naming it (and calling it a leftover of
  Once's services topology when it carries Once's tag). Needs `services:read`
  on the Nodes credential, now listed in its scopes, not probed at enable -
  without it the check is skipped. Ticket 03 adds the probe and the
  degraded-check reporting.
- Accepted: like the nodes device sweep, "Once's own" means "carries Once's
  tag", so two installs sharing a tag in one tailnet would sweep each
  other's Services on leave.
