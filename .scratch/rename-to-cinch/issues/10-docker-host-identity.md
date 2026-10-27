# 10: Docker and host identity say cinch

**What to build:** A fresh install creates only cinch-named things: the `cinch` label key, the default namespace `cinch` (so `cinch-proxy`, `cinch-app-*`, data volumes, the background service), `cinch-admin` and its socket/runtime dir, `/var/lib/cinch` backup paths, renamed state, probe, userstats, recovery and backup-archive file names, the systemd description, the `host.cinch.` launchd label, and the proxy pinned to `cinch-02`. Nothing reads the old names (SPEC.md, Stored identity rows except Tailscale).

**Blocked by:** 09 (The binary is cinch)

**Status:** ready-for-agent

- [ ] Every non-Tailscale row of the SPEC.md Stored identity table is applied
- [ ] Unit and integration test fixtures use the new names
- [ ] `make build`, `make test`, `make lint` pass
