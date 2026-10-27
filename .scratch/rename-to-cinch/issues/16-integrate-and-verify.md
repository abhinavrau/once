# 16: Integrate and verify the rename

**What to build:** The `rename-to-cinch` branch is proven whole: the full suite including integration is green, and the SPEC.md leftover check prints only English "once", Basecamp references and historical records. The graph is updated.

**Blocked by:** 15 (Docs say Cinch)

**Status:** ready-for-agent

- [ ] `make build`, `make test`, `make lint` pass
- [ ] `make integration` passes (under the integration flock)
- [ ] The SPEC.md leftover check is run and every remaining hit is judged allowed; the result is recorded in this ticket
- [ ] `graphify update .` has run
