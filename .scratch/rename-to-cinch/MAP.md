# Wayfinder map: Rename Once to Cinch

Labels: wayfinder:map

## Destination

A build-ready rename spec (SPEC.md in this directory): a table of every old ->
new identifier, so one agent session can carry it out. Doing the rename is a separate effort; this map ends
at the spec.

## Notes

- Settled while charting:
  - Full rename, including stored identity: binary, Go module, GitHub repo,
    Docker label key, default namespace, container names, `ONCE_*` env vars,
    state dir, systemd unit, Tailscale tag.
  - No migration of live installs: cinch does not read or adopt anything
    `once` left behind, and needs no compatibility code.
  - Cinch is a hard fork: no more merges from basecamp/once.
  - Cinch publishes its own releases at abhinavrau/cinch, and self-update
    points there. Today `internal/version/updater.go:17` still points at
    basecamp/once.
  - Keep the MIT license and credit "forked from basecamp/once" in the README.
  - The name "cinch" is final; a collision check only flags risk.
  - The project's domain is `cinch.host` (owned by the maintainer).
- Inventory taken while charting (2026-10-26):
  - Module `github.com/basecamp/once`; about 2.3k word hits for "once".
  - Label key `once` (internal/docker/label.go:9); default namespace `once`
    (internal/docker/namespace.go:26).
  - About 14 `ONCE_*` env vars; state dir `~/.local/state/once`
    (internal/logging/log.go).
  - Proxy image `abhinavrau/kamal-proxy:once-02` (internal/docker/proxy.go:21).
  - Installer at installer/ deploys `get.once.com` (Basecamp-owned) with
    `REPO='basecamp/once'`.
- History was squashed on 2026-10-26; the pre-squash history is at tag
  `backup/pre-squash`.
- Vocabulary: CONTEXT.md. Skills: /grilling and /domain-modeling for HITL
  tickets; /research for AFK research tickets.

## Decisions so far

- [Research: is "cinch" free to use?](issues/01-research-is-cinch-free.md): clear in distro repos, but cinch.sh (self-hosted CI) and cinchcli.com ship `cinch` binaries via curl; never overwrite a foreign `cinch`
- [Distribution and release identity](issues/04-distribution-and-release-identity.md): static `install.sh` at get.cinch.host (Go server deleted), foreign-`cinch` guard, first release v0.2.0, repo renamed to abhinavrau/cinch and detached from the fork network, proxy retagged `cinch-02`
- [Which text and docs get renamed?](issues/05-which-text-gets-renamed.md): history stays as written plus ADR-0019 name table; all living text renamed; ONCE -> CINCH text wordmark; Cinch/`cinch`/CINCH; identifier patterns, never a blind replace

## Not yet specified

- Local dev environment: workspace dir, graphify-out, treehouse.toml, and the
  deja index after the repo path changes.

## Out of scope

- Renaming Basecamp's catalog images (`ghcr.io/basecamp/once-campfire` etc.)
  or "ONCE" where it means Basecamp's product line: not ours to rename.
- Doing the rename: a separate effort that takes SPEC.md as input.
- [Research: which stored identity can be renamed in place?](issues/02-research-what-cannot-rename-in-place.md)
  and [How does the live install move from once to cinch?](issues/03-migration-path-for-the-live-install.md):
  closed, because live installs are not migrated.
