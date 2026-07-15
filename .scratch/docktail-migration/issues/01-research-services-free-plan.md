# Research: Tailscale Services on the free Personal plan

Status: done
Labels: wayfinder:research
Blocked by: (none)

## Question

Is the Tailscale Services feature (kb/1552) fully usable on the free Personal
plan, and with what limits? Specifically:

- Feature availability per plan (Personal / Personal Plus / paid tiers)
- Maturity: GA, beta, or behind a feature flag; any signals about API stability
- Service count limits per tailnet and per plan
- Any other constraints relevant to a small self-hosted server running a
  handful of apps (approval flow, tag requirements per plan)

Context: this is the hard go/no-go bar for replacing TSDProxy with docktail in
Once - if Services is unavailable or capped too low on the Personal plan, the
answer is no-go. Prefer primary sources: tailscale.com/kb, pricing page,
changelog/blog.

## Resolution

**Feature availability per plan.** Available on all plans, including the free
Personal plan. The launch post (Oct 28, 2025) states "Tailscale Services is
available today, on all plans, in public beta", and nothing since has
restricted it by plan. Note the Personal Plus tier was retired in the April 8,
2026 pricing update ("pricing v4"), which also made the free Personal plan more
generous (now up to 6 users, unlimited user devices).

**Maturity.** Generally available, no feature flag. The Tailscale changelog
records the beta launch on Oct 28, 2025 and GA on Jan 27, 2026 ("Use Tailscale
Services to decouple applications and services from the devices that host them
(generally available)"). kb/1552 presents it as GA (last validated Feb 2026).
Requires Tailscale v1.86.0+ on the host. GA is a reasonable API-stability
signal; the one known behavioral change in flight was routing to local-network
destinations landing in 1.92.

**Service count limits.** No published or enforced per-plan cap on the number
of Services today. kb/1552 has no numeric limits, and the pricing page and
pricing FAQ list no Services quota for any tier. The pricing-v4 post says
Tailscale "will also be rolling out self-serve pricing for increased device and
Service limits in the coming months", so plan-dependent Service limits are
planned but not yet defined or enforced; the beta announcement promised a
"legacy exception" for usage defined during the open beta. The adjacent limit
that does exist is tagged resources: the Personal plan includes 50 tagged
devices ($1/month each beyond that), and the FAQ states Tailscale is "currently
not enforcing the hard limits or overages of ... tagged resources". A Once
server is 1-2 tagged devices, far under that.

**Other constraints for a small self-hosted server.** (1) Service hosts must
use a tag-based identity; user-authenticated devices cannot host Services. Tags
work on the free plan, so this is a setup step, not a blocker. (2) Each
advertised Service host must be approved by an Owner/Admin/Network admin unless
autoApprovers are configured in the tailnet policy; on a personal tailnet the
user is the Owner, and docktail/Once can document an autoApprovers snippet to
make it hands-off. (3) Defining Services requires Owner/Admin/Network admin
permissions (true for the account owner on a Personal tailnet). (4) TCP is the
only supported transport for TUN-mode endpoints (UDP only via Linux layer 3
endpoints); fine for HTTP apps behind the Once proxy. (5) Service virtual IPs
accept incoming connections only.

**Bottom line.** Pass. Tailscale Services is generally available (GA since Jan
27, 2026) on every plan including the free Personal plan, with no service count
cap published or enforced today and no paid-tier gating of any prerequisite
(tags, grants, MagicDNS all work on Personal). The requirements that do exist
(tagged host identity, one-time or auto-approved host approval) are workflow
constraints Once can handle in setup docs or config. The residual risk is
forward-looking, not current: Tailscale has said plan-dependent Service limits
and self-serve pricing are coming, so a future cap on the Personal plan is
possible, but with a promised legacy exception for existing usage and a
handful-of-apps footprint this does not move the go/no-go bar.

### Sources

- https://tailscale.com/kb/1552/tailscale-services
- https://tailscale.com/blog/services-beta
- https://tailscale.com/changelog
- https://tailscale.com/blog/pricing-v4
- https://tailscale.com/pricing
- https://tailscale.com/pricing/faq
