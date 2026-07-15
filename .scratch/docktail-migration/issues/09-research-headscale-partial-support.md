# Research: headscale - Services without serve/Funnel, and policy API

Status: done
Labels: wayfinder:research
Blocked by: (none)

## Question

Two follow-ups on the headscale findings from issue 03:

1. Does headscale support (or could it support) Tailscale Services
   independently of serve/Funnel? Issue 03 found serve/Funnel depend on
   control-plane HTTPS cert machinery headscale lacks - is the Services (VIP
   services) protocol surface separable, i.e. could Services advertisement
   work against headscale while serve/Funnel remain unsupported? Check issue
   #2845's discussion and any design docs for what Services actually requires
   from the control plane (capability grants, VIP allocation, cert issuance
   for svc names).

2. Does headscale's REST API support updating the ACL/policy like Tailscale's
   /api/v2/tailnet/{t}/acl does? Headscale has policy modes (file vs
   database); does its API expose policy read/write (e.g. /api/v1/policy),
   with what auth, and does it accept the same HuJSON policy format including
   autoApprovers and grants sections?

Context: feeds the deferred go/no-go (issue 06). If headscale's policy API
mirrors Tailscale's, Once's potential api-write ACL automation (issue 08)
could keep working on the headscale seam; if Services can never work against
headscale even in principle, the fake-seams test strategy is locked in.
Primary sources: juanfont/headscale repo (issues, docs, api), headscale.net
docs, Tailscale Services protocol docs.

## Resolution

### Part 1: Is Services separable from serve/Funnel against headscale?

In principle yes, in practice no. The control-plane surface Services needs is
architecturally separable from the HTTPS cert machinery that blocks
serve/Funnel, but headscale implements none of it and its policy engine
actively rejects Services vocabulary.

What Services requires from the control plane (per Tailscale's docs,
kb/1552/tailscale-services):

- A service registry: `svc:` named service definitions created via admin
  console or API, each with a MagicDNS name and a resource definition.
- TailVIP allocation: the control plane assigns each service a virtual IP
  (v4 + v6) from a dedicated range and publishes the `svcname.<tailnet>.ts.net`
  DNS name.
- An advertisement/approval flow: tagged hosts advertise a service (via
  `tailscale serve --service=...`, serve set-config, or Linux-only `--tun`
  L3 mode) and the control plane holds them pending until approved manually
  or by an `autoApprovers.services` policy rule.
- Netmap distribution: peers (clients 1.94+) auto-discover service routes,
  VIPs, and packet-filter entries from MapResponses; policy grants can target
  `svc:` destinations.
- TLS cert issuance for service DNS names: only for HTTPS serve mode. TCP
  forwarding and L3 TUN mode need no certs.

So the cert-issuance dependency that issue 03 found fatal for serve/Funnel
applies only to the HTTPS flavor of Services; a control plane could support
TCP/TUN-mode Services advertisement without any cert machinery. But headscale
today has none of the other pieces either: no service registry, no VIP
allocation, no advertisement/approval state. Issue #2845 remains a bare
feature request - one comment noting Services left beta upstream, no design
doc, no linked PRs, and repo search finds no VIP-services implementation work
anywhere. Moreover headscale's policy parser is strict: unknown fields fail
with ErrUnknownField (hscontrol/policy/v2/types.go, unmarshalPolicy), the
AutoApprovers struct has only `routes` and `exitNode` (no `services`), and
parseAlias recognizes no `svc:` prefix, so any policy that mentions Services
is rejected outright - the same treatment the v0.29.0 changelog gave `funnel`
caps (types.go maps tailcfg.NodeAttrFunnel to tracking issue #2527).

### Part 2: Does headscale's API expose policy read/write in Tailscale's format?

Yes for read/write, with caveats on endpoint shape and mode.

- Endpoints: GET `/api/v1/policy`, PUT `/api/v1/policy`, and POST
  `/api/v1/policy/check` (validate-only). Present in the released v0.29.2 via
  gRPC-gateway (proto/headscale/v1/policy.proto, headscale.proto HTTP
  bindings) and preserved byte-for-byte on main after the migration to a
  native huma-based REST API (hscontrol/api/v1/policy.go).
- Auth: `Authorization: Bearer <API_KEY>` with keys minted by
  `headscale apikeys create` (90-day default expiry). No OAuth.
- Mode gating: GET works in both policy modes (file and database). PUT (and
  effective use of check) requires `policy.mode: database`; in file mode PUT
  returns 400 "policy update is disabled". So api-write automation dictates
  DB mode on the harness.
- Payload shape differs from Tailscale: headscale wraps the document as JSON,
  `{"policy": "<HuJSON string>"}`, returning `{policy, updatedAt}` - unlike
  Tailscale's `/api/v2/tailnet/{t}/acl` which takes the raw HuJSON body with
  If-Match etag concurrency control (headscale has no etag support).
- Document format: same HuJSON policy file format. `acls`, `grants` (including
  app grants and `via`), `autoApprovers.routes`/`exitNode`, `tagOwners`,
  `ssh`, and a subset of autogroups are accepted; grants are the recommended
  path. Not accepted (rejected, not ignored, due to strict unknown-field
  parsing): `postures`/`srcPosture`, IP sets, `autoApprovers.services`, `svc:`
  targets, and funnel/ingress caps.

### Implications for Once

- Test seam: the fake-the-seams strategy from issue 03 stays locked in for
  anything Services-shaped. Even though Services is separable from cert
  machinery in principle, headscale offers no partial Services surface to
  test against and no near-term prospect of one (#2845 is unowned, milestoned
  v0.34.0 with current release v0.29.2). The headscale harness remains useful
  only for the plain registration/route path.
- ACL automation: an api-write ACL automation (issue 08) can work on the
  headscale seam for the sections both sides share (grants, acls,
  autoApprovers.routes, tagOwners, ssh) provided the harness runs headscale
  in database policy mode and Once abstracts the transport difference (PUT
  wrapped-JSON + bearer key vs POST raw HuJSON + etag). But it cannot round-
  trip a policy containing Services sections: headscale will 400 on
  `autoApprovers.services` or `svc:` grants. If the docktail replacement's
  ACL writes include Services vocabulary, the headscale seam cannot validate
  them - only the shared-subset writes.

### Sources

- https://github.com/juanfont/headscale/issues/2845 (Services feature
  request: no design doc, no PRs, one comment; fetched via gh)
- https://tailscale.com/kb/1552/tailscale-services (Services requirements:
  registry, TailVIP, MagicDNS, approval/autoApprovers, HTTPS-only cert
  provisioning, TCP/L3 modes, client 1.94+ route discovery)
- https://headscale.net/stable/ref/api/ (REST API: bearer API-key auth,
  /api/v1 base, swagger)
- https://headscale.net/stable/ref/policy/ and
  docs/ref/policy.md@main (same HuJSON format, ACLs + Grants + SSH,
  limitations: no postures, no IP sets, autogroup subset)
- juanfont/headscale proto/headscale/v1/policy.proto and headscale.proto
  @v0.29.2 (GetPolicy/SetPolicy/CheckPolicy mapped to GET/PUT
  /api/v1/policy, POST /api/v1/policy/check)
- juanfont/headscale hscontrol/api/v1/policy.go@main (huma REST handlers:
  PUT DB-mode-only, {"policy": string} body, SSH-rule verification on set)
- juanfont/headscale hscontrol/policy/v2/types.go@main (strict
  ErrUnknownField parsing; AutoApprovers has only routes/exitNode; parseAlias
  has no svc: case; NodeAttrFunnel rejected pointing at issue #2527)
- .scratch/docktail-migration/issues/03-research-headscale-services.md
  (prior finding: Services/serve/Funnel unsupported, milestones, changelog)
