# Assemble the docktail integration spec (PRD)

Status: done
Labels: wayfinder:grilling
Blocked by: 04, 06

## Question

If the decision is go: what exactly gets built? Produce a build-ready PRD.md
in .scratch/docktail-migration/ covering at least:

- Container topology: docktail container, optional Once-managed sidecar, host
  tailscaled detection; naming under the once namespace
- Enable/disable flow: credential prompts, validation, what replaces the
  registration probe and rejection surfacing
- ACL provisioning via api-write (decided, issue 06/08): 4-section hujson
  patch with diff + confirm, validate + If-Match, snippet fallback, GitOps
  warning; whether to offer the oauth_keys bootstrap flow (mint runtime +
  policy clients from one credential) - pin down scope attenuation first
- Label schema mapping: Once app settings to docktail.service.* /
  docktail.funnel.* labels; admin app exposure as a Service
- URL discovery for CLI/TUI (from issue 04's findings)
- Test strategy: fake CLI/socket + stubbed cloud API seams (or headscale, per
  issue 03)
- Teardown of the old tsdproxy pieces (clean break - removal only, no
  migration)

Resolve by graduating the map's remaining fog into the PRD via grilling; this
ticket closes when the PRD is reviewed by the human and the map has no open
fog toward the destination.

## Comments

2026-07-13: RESOLVED - PRD.md written in this directory (labeled
ready-for-agent) via /to-spec, synthesizing the map's decisions and the
current codebase. Test seams confirmed with the human: (1) the existing
httptest baseURL seam extended to all Tailscale cloud API calls, (2) one new
local-tailscaled interface seam; headscale harness kept for a minimal
sidecar-join test. All remaining fog (funnel mapping, admin exposure,
enable-flow UX, teardown, spec structure) graduated into the PRD. One open
implementation-time check is recorded in the PRD's Further Notes (OAuth
scope attenuation, gating the bootstrap-credential path).
