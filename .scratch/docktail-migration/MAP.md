# Wayfinder map: Replace TSDProxy with docktail

Labels: wayfinder:map

## Destination

A locked go/no-go decision on replacing TSDProxy with docktail
(github.com/marvinvr/docktail) as Once's tailscale integration, and - if go - a
build-ready spec (PRD.md in this directory). Implementation is a separate
effort.

## Notes

- Docktail checkout for reference: /tmp/docktail (clone of github.com/marvinvr/docktail)
- Current architecture: docs/adr/0001-tailscale-integration.md; prior feature work in .scratch/tailscale-integration/
- Skills: /grilling and /domain-modeling for HITL tickets; /research for AFK research tickets
- Standing decisions from the charting session (not tickets):
  - Motivation: device-slot consumption, native Tailscale Services as the first-party primitive, operational simplicity
  - tailscaled source: support both - use host tailscaled when present, else a Once-managed tailscale/tailscale sidecar
  - Testing: fake the seams (fake tailscale CLI/socket + stubbed cloud API, byte-compatible request assertions), unless research finds headscale supports Services
  - Existing tsdproxy installs: clean break - no migration tooling; users re-enable tailscale after upgrade
  - Hard go/no-go bar: Tailscale Services must work on the free Personal plan with a workable service cap
  - OAuth credentials stay mandatory in Once's setup flow
  - Provisional ACL-UX preference (pending issue 08): api-write - Once offers to patch the tailnet policy itself, with user confirmation

## Decisions so far

<!-- one line per closed ticket: gist + link -->

- [Research: Tailscale Services on the free Personal plan](issues/01-research-services-free-plan.md) - Pass: Services is GA (Jan 2026) on all plans including free Personal, no service cap published or enforced; only constraints are tagged host identity and (auto-approvable) host approval
- [Research: Services + Funnel from a userspace-networking sidecar](issues/02-research-userspace-sidecar.md) - Yes: an unprivileged tailscale/tailscale sidecar (TS_USERSPACE=true, no TUN/NET_ADMIN/host networking) on the shared app bridge network can advertise Services and serve Funnel via docktail; needs only v1.86+, tags, approval, funnel nodeAttr - docktail's documented sidecar compose is over-privileged and can be trimmed
- [Research: headscale support for Tailscale Services](issues/03-research-headscale-services.md) - No: headscale supports neither Services nor serve/Funnel (tracked gap, several releases away at best); the headscale seam dies for Services flows and the fake-the-seams test strategy stands
- [Research: service URL discovery without tsdproxy's REST API](issues/04-research-url-discovery.md) - Compute `https://{app}.{suffix}` from the service name plus `.CurrentTailnet.MagicDNSSuffix` read off the local tailscaled at enable (no probe, no cloud call); verify advertised via `tailscale serve status --json` and approved via cloud `GET .../services/{svc:name}/devices` (SaaS only)
- [Research: updating the tailnet ACL via the Tailscale REST API](issues/08-research-acl-api-write.md) - Viable and reasonably safe: GET/POST /acl round-trips HuJSON with comments intact, ETag If-Match prevents clobbering, tailscale/hujson enables a targeted 4-section merge; use a one-shot policy_file-scoped credential (never the runtime client), diff + confirm before POST, keep the printed snippet as fallback (GitOps-managed tailnets silently overwrite API writes)
- [Research: headscale - Services without serve/Funnel, and policy API](issues/09-research-headscale-partial-support.md) - Services is separable from cert machinery in principle but headscale has zero Services surface and its strict parser rejects svc:/autoApprovers.services, so fake-seams stays locked in; headscale does expose GET/PUT /api/v1/policy (bearer key, DB mode for writes) accepting the same HuJSON for the shared subset, so ACL automation works on the seam minus Services vocabulary
- [Research: tailnet-side prerequisites for docktail](issues/05-research-tailnet-prereqs.md) - Setup burden rises from 1 OAuth scope + 1 tagOwners line to 3 scopes + up to 4 ACL sections (tagOwners x2, autoApprovers.services, svc: grants, funnel nodeAttr); Once can probe scopes/tags and detect pending approvals, but autoApprovers and access grants are manual ACL edits - and the default allow-all policy does not reach Services without an explicit svc: grant (burden later dissolved by the ACL API-write finding)
- [Decision: go/no-go on replacing TSDProxy with docktail](issues/06-go-no-go-decision.md) - GO. Rationale: the ACL burden is removed by driving the Tailscale policy API (api-write with diff + confirm); all other bars cleared. ACL-UX locked as api-write; OAuth clients can also be minted via API (oauth_keys scope, May 2025) so setup can shrink to one bootstrap credential
- [Assemble the docktail integration spec (PRD)](issues/07-spec.md) - PRD.md written (ready-for-agent): topology (once-docktail + optional unprivileged sidecar), docktail.* labels, api-write enable flow, computed URLs, two test seams (httptest cloud API + local-daemon interface), minimal headscale sidecar-join test, clean-break teardown

## Not yet specified

(none - all fog graduated into PRD.md; the destination is reached. The one
implementation-time check, OAuth scope attenuation gating the bootstrap
flow, is recorded in the PRD's Further Notes.)

## Out of scope

- Migration tooling for existing tsdproxy installs (clean break decided during charting)
- Implementation of the swap itself - this map ends at the spec
