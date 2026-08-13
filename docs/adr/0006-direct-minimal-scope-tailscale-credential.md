# Direct Minimal-Scope Tailscale Credential

Supersedes ADR-0004 (bootstrap-only credentials).

Amended by [ADR-0013](0013-runtime-credential-from-the-environment.md): the
setup command line below is now
`ONCE_TAILSCALE_CLIENT_SECRET=... once tailscale enable --client-id ...`, and
`--client-secret` is refused.

ADR-0004's premise was that a single `oauth_keys`-scoped bootstrap credential
could mint the narrower clients Once needs, so the user's admin-console work
shrank to one scope checkbox and the powerful credential was never persisted.
The Tailscale API disproved the premise: creating an OAuth client via
`POST /tailnet/{t}/keys` enforces scope attenuation - the creating actor can
only grant scopes it itself holds (the API rejects with `actor cannot set
scopes: [...]`). A working bootstrap credential would therefore need
`oauth_keys` plus the union of every delegated scope, making it strictly more
powerful than everything it mints - and it keeps existing in the tailnet after
enable unless the user remembers to revoke it. The blast radius of the system
is defined by the most powerful credential that exists, not the one Once
chooses to persist. Attenuation also kills the automated policy write's value:
the admin console refuses to create a tagged credential until the tag exists in
`tagOwners`, so the user hand-edits the policy file no matter what.

We decided the user creates the runtime credential **directly**: one OAuth
client with exactly the scopes Docktail and the sidecar run with (`auth_keys`,
`devices:core`, `services`), tagged with `tag:once` (or `--tag`). It is the
most powerful Tailscale credential that ever exists in this design. Setup is:
merge the printed policy snippet (`once tailscale policy`), create the tagged
client in the admin console, run `once tailscale enable --client-id ...
--client-secret ...`. The enable-time probe remains the enforcement for scopes
and the tag; a tag-shaped probe failure prints the policy snippet
(`docker.ErrTagNotInPolicy`).

## Consequences

- The bootstrap minting machinery, the one-shot `policy_file` client, the
  API policy write (HuJSON patch, diff, consent prompt, GitOps warning), and
  the `--yes`/`--no-policy-write` flags are deleted. The policy file is always
  merged by hand from the snippet; `once tailscale policy` prints it without
  needing Docker.
- The TUI overlay is a single form (ID, secret, tag) with no consent or
  snippet states; ctrl+p copies the policy snippet.
- Setup is two admin-console round trips (policy file, then credential),
  ordered by the console itself: the tag picker only offers tags already in
  `tagOwners`, so the ordering mistake ADR-0004 feared cannot be made.
