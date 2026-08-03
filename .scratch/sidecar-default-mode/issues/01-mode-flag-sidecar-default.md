# 01 - `--mode` flag with sidecar default and sticky re-enable

**What to build:** Per ADR-0007, `once tailscale enable --mode host|sidecar`
(default `sidecar`) replaces host-daemon auto-detection. A user enabling
Tailscale gets the sidecar unless they explicitly ask for host mode. With
`--mode` unset, an already-enabled install keeps its stored mode, so re-enable
stays idempotent for existing host-mode installs; an explicit `--mode` always
wins, and changing mode rebuilds the topology exactly as changed credentials
do. A fresh enable that defaults to sidecar while a host tailscaled socket
exists prints a one-line notice pointing at `--mode host` (never on
re-enables).

**Blocked by:** None - can start immediately.

**Status:** done

- [x] Fresh enable with no `--mode` boots the sidecar even when a host tailscaled socket is present, and prints the one-line `--mode host` notice in that case
- [x] Fresh enable with no host socket boots the sidecar with no notice
- [x] Re-running enable with `--mode` unset on an existing host-mode install keeps host mode and does not rebuild
- [x] Explicit `--mode` differing from the stored mode rebuilds the topology (sidecar state volume retained, matching the changed-credentials path)
- [x] `--mode` rejects values other than `host` and `sidecar`
- [x] Host-daemon auto-detection no longer decides the mode anywhere
- [x] Integration suite (headscale control seam) still passes - the seam always ran the sidecar, so the default aligns

## Comments

- 2026-08-03 (coordinator): verified implemented on main - feature present in code and its integration tests green in the 2026-07-30 wave-4 runs. Done.
