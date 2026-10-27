# Which text and docs get renamed?

Status: done
Assignee: arau
Labels: wayfinder:grilling
Blocked by: (none)

## Question

Beyond code identifiers, what text changes?

- Historical records: ADRs in docs/adr, closed tickets and PRDs in .scratch,
  the squashed commit history. Rewrite them, or leave them as records of their
  time?
- Living docs: README, AGENTS.md, CONTEXT.md (the glossary defines "Once"),
  CLI help, TUI copy.
- Brand: the "ONCE" wordmark on the handoff page and any TUI title. Is it a
  text swap, or does cinch need its own mark?
- Capitalization: is the product "Cinch" in prose and `cinch` as the command?

## Comments

Resolved (grilling, 2026-10-27):

1. Historical records stay as written: ADRs 0002-0018, closed `.scratch`
   tickets and PRDs, commit history. Add ADR-0019 "Once is renamed Cinch"
   carrying the old -> new name table so old records stay readable.
2. All living text is renamed: README, AGENTS.md, CONTEXT.md glossary, CLI
   help, TUI copy, code comments, test names, the password-manager entry name
   (`once backups - <host>` -> `cinch backups - <host>`), and open `.scratch`
   tickets.
3. Wordmark: text swap "ONCE" -> "CINCH" in the three places it appears
   (internal/ui/styles.go:76 breadcrumb, internal/ui/custody.go:49,478
   Recovery Kit header, internal/handoff/page.go:91 handoff page). No new mark.
4. Capitalization: "Cinch" in prose, `cinch` for the command and identifiers,
   "CINCH" only in the wordmark spots.
5. No blind find-and-replace: "once" is also an English word in the docs
   (e.g. CONTEXT.md:21,124). The spec names the identifier patterns to rename
   (`once-` prefixes, the `once` label key, `ONCE_*`, `.once-`, `/once`,
   "Once" as a name); every remaining lowercase "once" gets checked by hand.
   The final check ignores English uses and Basecamp catalog references.
