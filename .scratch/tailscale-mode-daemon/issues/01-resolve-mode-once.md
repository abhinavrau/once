# 01 - Resolve the Tailscale mode once per enable

**What to build:** Enabling Tailscale resolves the effective mode exactly once,
through the single implementation of the ADR-0007 rule, and hands the resolved
mode down. Today the rule lives twice - in the mode resolver and again inline
in the Tailscale Enable lifecycle - synchronized only by a comment.

The resolver returns the resolved mode plus whether this is a fresh enable
(no Docktail container yet). The Enable Tailscale workflow on Namespace calls
it once, right where it already decides whether the host-mode pre-flight must
run, then stamps the resolved mode into the settings before validation and
boot. The Tailscale Enable lifecycle requires an already-resolved mode and
loses its own copy of the rule, including its empty-mode defaulting.

The "--mode host" fresh-enable notice moves up with the resolution: it is a
pure rule (fresh enable, no mode requested, host socket present) computed in
the workflow where warnings already assemble, leading the warnings list as its
doc comment promises. The Tailscale Enable lifecycle then returns only an
error.

The host-mode pre-flight's daemon comes from the one daemon-binding function
instead of being constructed directly, so every mode-to-daemon choice goes
through a single door. This matters because multiple VPN providers are coming:
mode resolution and daemon binding are provider-shaped, and each must stay a
single function (no provider abstraction yet).

Accepted behavior change: a corrupt or legacy label with no stored mode,
re-enabled with unchanged credentials, now resolves to sidecar and rebuilds
the topology (stamping the mode, keeping the state volume) instead of
silently ensure-running with a modeless label - ADR-0007's self-heal on every
enable, not just on rebuilds.

**Blocked by:** None - can start immediately.

**Status:** in-review

- [x] The ADR-0007 rule (explicit wins, empty keeps stored, fresh defaults to
      sidecar, corrupt stored mode reads as sidecar, real inspect failure is
      an error) has exactly one implementation, called once per enable
- [x] The Tailscale Enable lifecycle takes a resolved mode, returns only an
      error, and contains no mode defaulting of its own
- [x] The fresh-enable notice is computed in the Enable Tailscale workflow as
      a pure helper and leads the returned warnings; explicit modes and
      re-enables never produce it (unit-tested via the socket-dir override)
- [x] The host-mode pre-flight's daemon is bound through the same function as
      every other daemon binding
- [x] A modeless stored label with unchanged credentials rebuilds to sidecar
      and the rebuilt label records the mode
- [x] Existing precondition-ordering unit tests still pass without a Docker
      daemon (explicit host mode must not touch the Docker client before the
      pre-flight)
- [x] Integration tests updated in the same change: the boots-topology test
      asserts the notice through the Enable Tailscale workflow's warnings
      (admin-server pattern from the teardown/UI tests), the host-mode test's
      sticky and rebuild semantics stay pinned by container-identity and
      label asserts, and all direct Enable call sites compile against the
      error-only return

## Comments

**2026-07-29** - branch `tailscale-mode-01-resolve-mode-once`, worktree
`/home/arau/code/once/.claude/worktrees/agent-a6f604b80838a3d8b`.

`effectiveMode` became `resolveMode(ctx, requested) (mode, fresh, error)`, the
only implementation of the ADR-0007 rule (an unrecognized stored mode now reads
as sidecar too, not just an unparseable label). `EnableTailscale` calls it once,
stamps the resolved mode into the settings, binds the host-mode pre-flight's
daemon through `daemonFor`, and leads its warnings with the notice from the pure
`hostModeNotice(requested, fresh)` helper; `Tailscale.Enable` now requires a
resolved mode and returns only an error.

Integration: the boots-topology test drives `EnableTailscale` (via a shared
`startAdminServer` helper) and asserts the notice leads the warnings, plus the
accepted self-heal (a modeless label rebuilds to sidecar with the mode stamped)
and the sticky unset-mode re-enable; the host-mode test dropped its notice
asserts and passes resolved modes. `make test` and `make lint` pass; the
integration suite was not run locally (the coordinator runs it) but compiles and
vets clean.
