# 03 - Bind settings and daemon once per read operation

**What to build:** Every Tailscale read path binds its daemon through one
private function instead of five copy-pasted two-line binders. A session
resolver loads the stored settings and returns them together with the daemon
they imply, and the five read paths (service statuses, shadow warnings,
service verify, funnel-activation wait, domain-suffix read) all start with
that one call.

This is not the enable-time mode resolution (ticket 01) - read paths never
see a requested mode; they bind from the stored mode as-is. Both concepts
funnel the actual daemon choice through the same mode-to-daemon function,
which is the single provider-shaped binding point the coming multi-provider
work will hang off (no provider abstraction now).

No public interface changes and no behavior changes: shadow warnings stay
best-effort (an unreadable daemon still yields no warnings rather than an
error), the domain-suffix read keeps its in-memory cache and lazy persist,
and the funnel wait keeps its node-host write-through. The persist paths
themselves are being centralized by the once-label-module feature, which is
why this ticket is blocked on it - both rewrite the same call sites.

**Blocked by:** once-label-module/02 (cross-feature: the domain-suffix and
funnel-wait call sites this ticket touches are the write-through persist
paths that ticket moves onto the label codec).

**Status:** in-review

- [x] One private session function owns the stored-settings-to-daemon
      binding; no read path calls the settings load and the daemon binder as
      a hand-written pair anymore
- [x] All five read paths compile through it with unchanged public interfaces
- [x] Shadow warnings remain best-effort and the domain-suffix cache and
      write-through behavior are unchanged (existing unit tests keep passing
      unmodified)
- [ ] Integration suite passes

## Comments

- 2026-07-30, branch `tailscale-mode-daemon-03-session-binder`: added the
  private `Tailscale.session(ctx) (TailscaleSettings, TailscaleDaemon, error)`
  method in `internal/docker/tailscale.go` and rewrote all five read paths
  (`ServiceStatuses`, `ShadowWarnings`, `VerifyServices`, `WaitForFunnelActive`,
  `DomainSuffix`) to call it instead of a hand-written `LoadSettings` +
  `daemonFor` pair. No public interfaces changed; shadow warnings still
  swallow the error and return nil; the domain-suffix cache and lazy persist,
  and the funnel wait's node-host write-through, are untouched. `make test`
  and `make lint` are clean. Ran the code-review skill (Standards + Spec axes
  against main) - both came back clean, no findings. The "Integration suite
  passes" checkbox is left unchecked here; pending the coordinator's
  post-merge integration run per instructions.
