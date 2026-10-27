# 18: get.cinch.host serves install.sh

**What to build:** `https://get.cinch.host` serves the static install script from a static host (GitHub Pages or Cloudflare, maintainer's choice), over HTTPS (SPEC.md, Distribution and step 12).

**Blocked by:** 17 (GitHub repo is abhinavrau/cinch)

**Status:** ready-for-human

- [ ] `curl -fsSL https://get.cinch.host` returns the install script from the main branch
