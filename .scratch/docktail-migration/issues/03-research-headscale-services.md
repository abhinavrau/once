# Research: headscale support for Tailscale Services

Status: done
Labels: wayfinder:research
Blocked by: (none)

## Question

Does headscale (juanfont/headscale) support Tailscale Services (VIP services)
today, or have concrete plans/PRs for it? Also: does it support `tailscale
serve`/Funnel-adjacent flows that docktail relies on?

Context: Once's integration test harness exercises the real registration flow
against a headscale control server (see integration/tailscale_test.go and
.scratch/tailscale-integration/issues/01-headscale-harness-control-seam.md).
If headscale lacks Services, that seam dies with docktail and the fake-the-
seams test strategy from the charting session stands; if headscale has grown
Services support, the harness may survive. Primary sources: headscale repo
(feature matrix, issues, PRs, changelog), headscale docs.

## Resolution

Direct answer: No. headscale does not support Tailscale Services (VIP
services) today, and it also does not support the serve/Funnel-adjacent
flows.

- Tailscale Services: not implemented. Tracked as juanfont/headscale issue
  #2845 "[Feature] tailscale services" (open, labeled tailscale-feature-gap).
  It is milestoned for v0.34.0, but the current release is v0.29.2
  (2026-07-01) and the intermediate milestones (v0.30.0 through v0.33.0) are
  all still open with no due dates, so v0.34.0 is several releases away. The
  issue has no linked PRs and no maintainer commitment; the only movement is
  the issue opener volunteering to write a design doc, and a comment
  (2026-04) noting Services left beta upstream. The Services feature does not
  appear in headscale's feature matrix at all. In short: a tracked gap, not a
  concrete plan.
- tailscale serve: not supported. The feature matrix lists Serve as
  unsupported; issue #1921 "[Feature] Support tailscale serve with HTTPS" is
  open, and #2527 "tailscale cert + serve tracking" is the umbrella. Serve
  depends on control-plane HTTPS cert issuance, which headscale does not
  ship.
- Funnel: not supported (feature matrix; issue #1040 closed as
  will-not-implement-yet). The v0.29.0 changelog even rejects policies using
  the `funnel` cap because "those features depend on machinery headscale does
  not yet ship."

Implication for Once's integration-test seam: the headscale harness
(integration/tailscale_test.go, per
.scratch/tailscale-integration/issues/01-headscale-harness-control-seam.md)
can only exercise the plain registration flow. Any docktail replacement built
on Tailscale Services, serve, or Funnel cannot be exercised against headscale
today or in the foreseeable release horizon. The headscale control seam dies
with docktail for Services-based flows, and the fake-the-seams test strategy
from the charting session stands. If the migration keeps a plain
register-and-route path, the headscale harness may still be worth keeping for
that path only.

Sources:

- https://headscale.net/stable/about/features/ (feature matrix: Serve and
  Funnel unsupported; Services absent)
- https://github.com/juanfont/headscale/issues/2845 (Tailscale Services
  feature request, open, milestone v0.34.0, no PRs)
- https://github.com/juanfont/headscale/issues/1921 (tailscale serve with
  HTTPS, open)
- https://github.com/juanfont/headscale/issues/2527 (cert + serve tracking)
- https://github.com/juanfont/headscale/issues/1040 (Funnel plans, closed)
- https://github.com/juanfont/headscale/releases (v0.29.2 latest,
  2026-07-01) and repo milestones (v0.30.0-v0.34.0 all open, no due dates)
- https://raw.githubusercontent.com/juanfont/headscale/main/CHANGELOG.md
  (v0.29.0 rejects `funnel` cap policies as unsupported machinery)
