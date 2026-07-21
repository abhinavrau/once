# 08 - Recreate the sidecar when the pinned tailscale image changes

**What to build:** Enable keeps a healthy existing once-tailscale sidecar
as-is, so when a Once release bumps the pinned tailscale/tailscale image the
running sidecar stays on the old one forever - only a crash-looping sidecar
gets rebuilt. Found while bumping the pin from v1.88.4 to v1.98.9: the only
way to pick up the new image was to remove the container by hand. Enable
(and/or the boot path) should notice the running sidecar's image differs from
the pin and recreate it, keeping the state volume so the node identity
survives - the same doctrine the crash-loop rebuild follows. Consider whether
once-docktail needs the same treatment for its pinned image.

**Blocked by:** None - can start immediately.

**Status:** needs-triage

- [ ] Re-running enable after a pin bump recreates the sidecar on the new image with the state volume (node identity) preserved
- [ ] A sidecar already on the pinned image is left untouched (no restart, no churn)
- [ ] Works on both the SaaS path and the headscale control seam
