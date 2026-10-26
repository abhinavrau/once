# Research: is "cinch" free to use?

Status: done
Labels: wayfinder:research
Blocked by: (none)

## Question

Would a CLI named `cinch` collide with anything a user might already have or
look for? Check:

- Existing executables or packages named `cinch` in common package managers
  (apt/Debian/Ubuntu, Homebrew, Arch, Nix, Go install), which would clash on PATH.
- Well-known projects or CLIs named cinch, especially in self-hosting, Docker,
  or devops.
- Whether `github.com/abhinavrau/cinch` and Docker Hub/GHCR names like
  `abhinavrau/cinch` are free.

The name is decided. This only flags risk; the maintainer judges it.

## Comments

Resolved. Findings: `.scratch/rename-to-cinch/research/01-is-cinch-free.md` on
branch `research/is-cinch-free` (not merged).

- Clear in Debian, Ubuntu, Arch, AUR, nixpkgs and Homebrew core.
- Two curl-installed third-party CLIs already ship a `cinch` binary:
  cinch.sh (self-hosted CI, Go, containers, `~/.cinch/bin`) and cinchcli.com
  (clipboard relay, `/usr/local/bin/cinch`, own Homebrew tap). Fedora/EPEL
  also package a dying `/usr/bin/cinch`.
- `github.com/abhinavrau/cinch` and Docker Hub `abhinavrau/cinch` are free;
  the Docker Hub org `cinch` is taken; cinch.sh and cinch.dev are registered.
- Implication for distribution: the installer and self-update must not
  overwrite a `cinch` binary that isn't ours.
