# 08: Go module is github.com/abhinavrau/cinch

**What to build:** Work starts on the `rename-to-cinch` branch (create it from main). The module path and every import use `github.com/abhinavrau/cinch`, and so does the version ldflag in the build. Rewrite only the exact import prefix, nothing else (SPEC.md, Code and build).

**Blocked by:** 07 (Proxy image is retagged as cinch-02)

**Status:** ready-for-agent

- [ ] `go.mod` declares `github.com/abhinavrau/cinch`
- [ ] No `github.com/basecamp/once` left in Go files or the Makefile
- [ ] `make build`, `make test`, `make lint` pass
