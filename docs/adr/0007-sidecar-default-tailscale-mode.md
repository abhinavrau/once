# Sidecar-Default Tailscale Mode

Supersedes the mode-selection paragraph of [ADR-0002](0002-docktail-tailscale-services.md).

ADR-0002 chose the tailscaled that Docktail drives by auto-detection: when the
host ran a tailscaled (its socket directory present), Once mounted it and
managed no daemon of its own; the sidecar existed only as the fallback. Field
use disproved the detection heuristic: a host daemon's presence is weak
evidence of intent. On a self-hoster's machine it is usually their personal
node - untagged, possibly logged into a different tailnet than the OAuth
credential, possibly too old for Services - and host mode depends on three
invariants Once can only observe, never repair: the node is tagged with the
credential's tag, the node is in the credential's tailnet, and the daemon is
v1.86+. When any of them fails, enable boots the full topology anyway and dies
90 silent seconds later in the service-advertisement verify, with the real
cause buried in the docktail container logs (in the motivating incident, the
host node was untagged and on an entirely different tailnet).

We decided the sidecar is the default and host mode is explicit opt-in:

- `once tailscale enable --mode host|sidecar` (default `sidecar`) replaces
  auto-detection; the TUI enable form gains the same selector.
- With `--mode` unset, an already-enabled install keeps its stored mode, so
  re-enable stays idempotent for existing host-mode installs; only fresh
  enables default to sidecar. An explicit `--mode` always wins, and changing
  mode rebuilds the topology exactly as changed credentials do.
- `--mode host` earns a fail-fast pre-flight at probe time - four hard
  failures, each with an actionable message: host socket present, node tagged
  with the credential's tag, node listed in the credential's tailnet
  (`GET /tailnet/-/devices`, a scope Once already holds), daemon v1.86+.
  Skipped on the headscale control seam alongside the OAuth probe, since the
  cloud check cannot run there.
- A fresh enable that defaults to sidecar while a host socket exists prints a
  one-line notice pointing at `--mode host`, so the escape hatch stays
  discoverable for genuinely tagged server nodes.

The accepted cost: host-daemon users who do not opt in spend one extra tailnet
device per Once install. We kept host mode rather than deleting it because a
properly tagged server node is a legitimate topology that saves that device
slot, and the mount logic it needs is small.
