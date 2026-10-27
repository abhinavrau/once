# 13: Every screen and message says Cinch

**What to build:** Users see Cinch everywhere: CLI help, errors and hints say `cinch <subcommand>`, product-name prose says Cinch, the CINCH wordmark appears in the TUI breadcrumb, the Recovery Kit header and the phone handoff page, and the password-manager entry is `cinch backups - <host>`. Product-name Go identifiers are renamed; English "once" identifiers stay (SPEC.md, Go identifiers and User-facing text). Review every remaining lowercase "once" by hand.

**Blocked by:** 12 (Environment variables are CINCH_*)

**Status:** ready-for-agent

- [ ] No `once` command hints or "Once" product names left in help, errors or TUI copy
- [ ] Wordmark is CINCH in all three places
- [ ] The SPEC.md keep-list identifiers are untouched
- [ ] `make build`, `make test`, `make lint` pass
