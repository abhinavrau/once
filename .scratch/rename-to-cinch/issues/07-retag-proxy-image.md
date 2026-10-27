# 07: Proxy image is retagged as cinch-02

**What to build:** The maintainer publishes the fork's kamal-proxy image under a cinch tag, so later tickets can pin it and the integration suite can pull it. Same digest, no rebuild (SPEC.md, Order of operations step 10).

**Blocked by:** None (can start immediately)

**Status:** ready-for-human

- [ ] `docker buildx imagetools create -t abhinavrau/kamal-proxy:cinch-02 abhinavrau/kamal-proxy:once-02` has run
- [ ] `docker pull abhinavrau/kamal-proxy:cinch-02` succeeds and its digest matches `once-02`
