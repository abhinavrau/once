# 19 - A proxy label rewrite must not depend on the registry

**What to build:** `once backup enable`, `backup disable`, and every other
proxy-settings rewrite succeed with the registry unreachable, reusing the
proxy image already on the host. The pull happens only when the image is
genuinely absent (true first boot). Today every rewrite goes through
remove-and-boot, and the boot path pulls unconditionally - `ImagePull`
contacts the registry even when the image is cached - so a label rewrite
fails outright exactly when the registry is least reachable: during
recovery. Surfaced by the acceptance work in issue 18.

The same decision applies to the admin and restic boot paths if they share
the shape: inspect first, pull only on absence.

**Blocked by:** None - can start immediately.

**Status:** ready-for-agent

- [ ] A settings rewrite with the proxy image present makes no registry
      call (unit test on the pull decision)
- [ ] A true first boot still pulls
- [ ] `backup enable` completes against a blocked registry on a host that
      has the image
- [ ] Lint, unit, and integration tests pass
