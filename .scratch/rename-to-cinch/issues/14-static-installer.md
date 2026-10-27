# 14: Static installer at get.cinch.host

**What to build:** `curl https://get.cinch.host | sh` installs the latest `cinch` from `abhinavrau/cinch` releases, and `sh -s -- <image>` also installs that app. If a `cinch` is already on PATH, the script reuses it only if `cinch version` carries `(cinch.host)`; otherwise it refuses, names the path, and never overwrites it. The Go installer server and its deploy config are deleted (SPEC.md, Distribution).

**Blocked by:** 13 (Every screen and message says Cinch)

**Status:** ready-for-agent

- [ ] One static install script; the Go server, its module, Dockerfile and deploy config are gone
- [ ] A shell test: a fake `cinch` on PATH without the marker makes the script refuse without writing anything
- [ ] A shell test: a fake `cinch` with the marker is reused
- [ ] `make build`, `make test`, `make lint` pass
