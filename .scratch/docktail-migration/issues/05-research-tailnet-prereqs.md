# Research: tailnet-side prerequisites for docktail

Status: done
Labels: wayfinder:research
Blocked by: (none)

## Question

What must exist on the user's tailnet before docktail works, and how much of
it can Once automate or validate? Specifically:

- OAuth client scopes docktail needs (see /tmp/docktail/docs/03-tailscale-admin.md
  and /tmp/docktail/tailscale/client.go) vs the scopes Once requests today for
  tsdproxy
- Required ACL configuration: auto-approvers for services, tag definitions,
  grants; what the out-of-box default ACL allows
- The host/sidecar node's `--advertise-tags` requirement and how tag ownership
  interacts with the OAuth client
- Which of these can be validated programmatically at enable time (Once
  currently validates tags and surfaces registration rejections - old issues
  18/20/21 in .scratch/tailscale-integration/issues/)
- Funnel prerequisites under this model (HTTPS certs enabled, funnel node
  attribute in ACL)

Context: defines the setup burden Once's enable flow must guide users through,
an input to the go/no-go decision. Primary sources: tailscale.com/kb, docktail
docs/code in /tmp/docktail.

## Resolution

Headline: the docktail model needs materially more tailnet-side setup than
tsdproxy. Today Once needs one OAuth scope and one tagOwners line; the docktail
model needs three OAuth scopes and up to four ACL sections (tagOwners x2,
autoApprovers.services, per-service grants, and a funnel nodeAttr), of which
the grants and autoApprovers cannot be pre-validated with the scopes docktail
uses. Most failures are detectable after the fact from API/CLI error seams,
but at least two ACL edits are unavoidable manual steps in every install.

### 1. OAuth scopes: docktail vs Once today

Once today (tsdproxy path, internal/docker/tailscale.go and
tailscale_validate.go): a single scope, Keys -> Auth Keys: Write (`auth_keys`),
on a tagged OAuth client. tsdproxy mints tagged, ephemeral, preauthorized auth
keys per app node; Once's enable-time probe mints-and-deletes a key of exactly
that shape.

docktail (docs/03-tailscale-admin.md, tailscale/client.go):
- General -> Services: Write (`services`) - PUT/GET/DELETE
  `/api/v2/tailnet/{t}/services/{svc}` to create/update/delete Service
  definitions and list advertising hosts (client.go putService, getService,
  listServices, listServiceHosts, deleteService).
- Devices -> Core: Write (`devices:core`) - device-side operations backing the
  Services host views and cleanup.
- Keys -> Auth Keys: Write (`auth_keys`) - only for the sidecar method, to
  mint (or use the OAuth secret as) the `TS_AUTHKEY` for the tailscale
  sidecar container. This is the one scope Once already requires, so it is
  the same client Once users have today plus two additional scopes.
- docktail can also run with zero credentials ("manual mode"): it still runs
  `tailscale serve --service=...` locally, but the user must create every
  Service definition by hand in the admin console.

Per kb/1215, tags on an OAuth client only matter for `devices:core`,
`auth_keys`, or `all` scopes; a client with `auth_keys` must be created with
one or more tags and can only mint keys for those tags (or tags they own via
tagOwners chains). Since Once controls the sidecar, an Once-shaped enable flow
needs a client with all three scopes, tagged with the host tag.

### 2. Required ACL configuration and the out-of-box default

Required sections (docktail docs 03, confirmed against kb/1552):
- `tagOwners`: both the host tag (e.g. `tag:server`) and the service tag
  (e.g. `tag:container`) must be defined; Tailscale rejects references to
  undefined tags, and GitOps-managed ACLs hard-fail on them.
- `autoApprovers.services`: maps a service name (`svc:<name>`) or service tag
  to the tags/users allowed to advertise hosts for it, e.g.
  `{"services": {"tag:container": ["tag:server"]}}`. Without it, every first
  advertisement of a Service sits pending until an Owner/Admin/Network admin
  approves it in the admin console Services tab.
- Grants: reaching a Service requires an explicit rule with a `svc:`
  destination (e.g. `{"src": ["autogroup:member"], "dst": ["svc:web"],
  "ip": ["443"]}`). The `*` wildcard dst expands to Tailscale IPs and
  approved subnet routes only, so the out-of-box allow-all policy does NOT
  grant access to Services (targets-and-selectors reference; services-beta
  blog: "users and devices on the tailnet must explicitly be granted access
  to a service via Grants"). Note docktail's own admin guide omits grants
  entirely - a real gap users would hit as "everything healthy but the
  service URL times out".

Out-of-box default ACL: allow-all between devices, but defines no tags, no
autoApprovers, no grants for `svc:` destinations, and no nodeAttrs. Every
docktail install therefore requires at least one manual ACL edit (tagOwners +
autoApprovers + a grant), versus one tagOwners line for tsdproxy today.

Services availability: all plans, public beta, hosts need Tailscale v1.86.0+
and Owner/Admin/Network admin to create definitions (kb/1552, services-beta).

### 3. `--advertise-tags` and tag ownership vs the OAuth client

Tailscale Services require the host node to have a tag-based identity; a
user-identity node cannot advertise a Service (kb/1552). docktail hits this at
`tailscale serve --service` time and prints a fix-it message
(service.go isUntaggedNodeError): host installs run
`sudo tailscale up --advertise-tags=tag:server --reset`; sidecars set
`TS_EXTRA_ARGS=--advertise-tags=tag:server` plus a `TS_AUTHKEY`.

Interaction with the OAuth client: the tag the node advertises must be one the
auth key (and therefore the OAuth client that minted it) is allowed to apply -
the client is created with a tag set, and can only mint keys for those tags or
tags owned by them via tagOwners. Separately, `autoApprovers.services` keys off
the HOST tag as the approver value, so the advertised tag, the OAuth client's
tag, and the autoApprovers value must all line up. In an Once-managed sidecar
this whole step is automatable: Once mints the tagged key itself, exactly as
the tsdproxy flow does today.

### 4. What Once can validate programmatically at enable time

Carry-overs from today (old issues 14/18/20/21):
- OAuth credential validity: token exchange against `/oauth/token` (already
  done in tailscale_validate.go).
- `auth_keys` scope + tag mintability: mint-and-delete probe (already done).
- Tag defined in tagOwners / registration acceptance: enforced by the control
  plane only at node registration; Once already scans tsdproxy logs for
  rejection signatures (VerifyRegistration). Same seam works for a sidecar.

New, possible with docktail's scopes:
- `services` scope + service-tag validity: probe PUT then DELETE of a
  throwaway Service definition (mirrors the existing mint-and-delete probe;
  an undefined tag in the payload fails the PUT with a clear API error).
- Advertisement approval state: after the host advertises, GET
  `/services/{svc}/devices` returns hosts with an `approvalLevel` field
  (client.go serviceHost), so Once can detect "advertised but pending
  approval" and tell the user exactly what to approve, or that
  autoApprovers is missing.
- Untagged host: `tailscale serve --service` fails with a recognizable error
  (docktail already string-matches it); moot if Once runs the sidecar and
  tags it itself.
- Funnel nodeAttr: detectable at funnel-enable time from the CLI error
  ("list of allowed nodes ... does not include", funnel.go isFunnelACLError);
  potentially pre-checkable from the node's own status capability map.

Not validatable with these scopes (reading the ACL needs an additional
policy-file scope Once should not require):
- `autoApprovers.services` presence (only detectable post-hoc as a pending
  approval).
- Access grants with `svc:` destinations. No API surface docktail uses can
  see them, and there is no runtime error: the service simply isn't reachable
  by users. This one is guidance-only.

### 5. Funnel prerequisites under this model

Per kb/1223: Tailscale v1.38.3+, MagicDNS enabled, HTTPS certificates enabled
for the tailnet, a `funnel` node attribute in the policy file, ports limited
to 443/8443/10000, available on all plans, public DNS may take ~10 minutes.

Under docktail's model, Funnel runs on the NODE (the tag:server host or
sidecar), not on the Service: the public URL is
`https://<host>.<tailnet>.ts.net`, not the service URL. So the nodeAttr grant
must target the host tag, e.g.
`{"nodeAttrs": [{"target": ["tag:server"], "attr": ["funnel"]}]}` - granting
it to the service tag does nothing (docktail docs 03). Funnel-for-Services is
listed by Tailscale only as a future direction (services-beta blog).
`autoApprovers.services` does not grant Funnel. Consequence for Once: all
funneled apps share the sidecar hostname and must be distinguished by port
(443/8443/10000) or path, versus per-app funnel hostnames with tsdproxy today.

### Prerequisite summary

| Prerequisite | Who sets it up | Automatable by Once | Validatable at enable | Notes |
|---|---|---|---|---|
| OAuth client with services + devices:core + auth_keys write, tagged | User, admin console | Manual-only (no API to create OAuth clients) | Yes: token exchange + per-scope probes (mint key, PUT/DELETE probe service) | Superset of today's single auth_keys client |
| Host tag + service tag in tagOwners | User, ACL edit | Manual-only | Partially: key-mint probe, service PUT error, registration-rejection scan | Same class of edit Once documents today, now two tags |
| Host node advertises host tag | Once (sidecar) or user (host install) | Yes in sidecar model (Once mints tagged key) | Yes: serve untagged-node error seam | Manual only if user brings their own tailscaled |
| Service definitions in control plane | docktail via API | Yes (services:write) | Yes | Manual mode exists but is a poor Once UX |
| autoApprovers.services rule | User, ACL edit | Manual-only | No pre-check; post-hoc via /services/{svc}/devices approvalLevel | Without it every new app needs console approval |
| Grants with svc: destinations | User, ACL edit | Manual-only | No; no error surface at all | Default allow-all does NOT cover Services; must be documented prominently |
| MagicDNS + HTTPS certs enabled | User, admin console toggle (one-time) | Manual-only | Indirect: cert provisioning errors at serve/funnel time | Already required for tsdproxy HTTPS today |
| funnel nodeAttr on host tag | User, ACL edit | Manual-only | Post-hoc: funnel CLI ACL error (docktail parses it); Once already surfaces the equivalent | Must target host tag, not service tag |
| First-advertisement approval | User, console (unless autoApprovers) | No | Detectable via approvalLevel | Eliminated by autoApprovers rule |

### Sources

- https://tailscale.com/kb/1215/oauth-clients (scopes, tags on OAuth clients)
- https://tailscale.com/kb/1552/tailscale-services (prerequisites, tag-based
  host identity, autoApprovers.services, grants, approval workflow, v1.86.0+)
- https://tailscale.com/kb/1223/funnel (funnel prerequisites, ports, plans)
- https://tailscale.com/docs/reference/targets-and-selectors (wildcard dst
  expands to IPs/subnet routes; svc: is a distinct selector)
- https://tailscale.com/blog/services-beta (all plans, public beta, explicit
  grants required, funnel-for-services as future work)
- /tmp/docktail/docs/03-tailscale-admin.md, /tmp/docktail/docs/02-installation.md
- /tmp/docktail/tailscale/client.go, service.go, funnel.go
- /home/arau/code/once/internal/docker/tailscale.go, tailscale_validate.go
- /home/arau/code/once/.scratch/tailscale-integration/issues/14, 18, 20, 21
