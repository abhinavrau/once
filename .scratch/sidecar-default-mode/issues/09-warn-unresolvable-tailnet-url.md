# 09 - Warn when a tailnet service URL does not resolve locally

**What to build:** When once displays a tailnet app's service URL (the status
view, after the service has had time to register - not the deploy path), it
attempts to resolve the URL's hostname from the machine the CLI is running on.
If resolution fails, it shows a warn-only note next to the URL: the name does
not resolve from this machine; if this machine is on the tailnet its MagicDNS
may be broken (suggest toggling `tailscale set --accept-dns=false` then
`=true`); other tailnet devices may be unaffected. A user whose local
tailscaled resolver is wedged (observed in the field: profile switch left an
empty DNS config, every query SERVFAILed) learns why the printed URL is dead
instead of concluding the deploy failed.

The check must never block or fail any operation, and the wording must stay
conditional: in sidecar mode the host is not required to be a tailnet member,
so a failed lookup can be a healthy state. Do not add the check to deploy-time
warnings - Docktail registers the Tailscale Service on a periodic reconcile,
so the name legitimately does not resolve at deploy-print time.

Reuse the existing warn-only DNS preflight seam (injectable resolver) so the
check is unit-testable without a tailnet.

**Blocked by:** None - can start immediately.

**Status:** ready-for-agent

- [ ] Status view resolves each tailnet-exposed app's service hostname from the local resolver and shows a warning next to the URL when the lookup fails
- [ ] Warning text is conditional (machine may simply not be on the tailnet) and includes the accept-dns toggle hint
- [ ] Lookup failure never blocks or errors the command; a short timeout bounds the check
- [ ] Unit tests cover resolve-ok (no warning) and resolve-fail (warning) via the injectable resolver seam
