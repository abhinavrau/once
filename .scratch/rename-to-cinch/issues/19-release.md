# 19: Release v0.2.0

**What to build:** The `rename-to-cinch` branch is merged, `v0.2.0` is tagged, and the release workflow publishes `cinch-<os>-<arch>` assets. A clean machine installs via `curl https://get.cinch.host | sh` (SPEC.md, step 13).

**Blocked by:** 18 (get.cinch.host serves install.sh)

**Status:** ready-for-human

- [ ] `v0.2.0` release exists on abhinavrau/cinch with all assets and checksums
- [ ] A clean-machine install works and `cinch version` prints `cinch v0.2.0 (cinch.host)`
- [ ] A release build's self-update reports it is up to date
