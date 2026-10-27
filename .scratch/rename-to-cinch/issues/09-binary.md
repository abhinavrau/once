# 09: The binary is cinch

**What to build:** A user runs `cinch`. The build produces `cinch` and release assets `cinch-<os>-<arch>`, self-update reads `abhinavrau/cinch` releases with the renamed temp file, and `cinch version` prints `cinch <version> (cinch.host)`, the marker the installer keys on (SPEC.md, Code and build).

**Blocked by:** 08 (Go module is github.com/abhinavrau/cinch)

**Status:** ready-for-agent

- [ ] The command root and the cmd entrypoint are named cinch
- [ ] `make dist` produces `cinch-<os>-<arch>` and a checksums file over them
- [ ] The updater's release URL and asset name are cinch's; its unit tests cover the new asset name
- [ ] `cinch version` output contains `(cinch.host)`, with a test
- [ ] `make build`, `make test`, `make lint` pass
