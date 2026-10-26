# 01 - Is "cinch" free to use?

Checked 2026-10-26. Name is decided; this only flags collision risk. Ranked by severity: PATH clash > same-domain project > name-only.

## TL;DR

- No `cinch` in Debian/Ubuntu, Arch, AUR, nixpkgs, or Homebrew core formulae. The default package managers on our target servers are clear.
- Two third-party CLIs ship a `cinch` binary today via curl installers. One is a **self-hosted, container-based CI tool** (closest overlap in domain and audience), the other is a clipboard sync tool installing to `/usr/local/bin/cinch`.
- One distro package ships `/usr/bin/cinch`: Fedora (<= 43) / EPEL 7, a Red Hat Jenkins provisioning tool. Retired in Fedora rawhide.
- `github.com/abhinavrau/cinch` and Docker Hub `abhinavrau/cinch` are free. The Docker Hub org `cinch` is taken (empty, since 2014).

## 1. PATH clashes (highest severity)

| Source | What | Binary / install path | Status | Severity |
|---|---|---|---|---|
| cinch.sh (ehrlich-b/cinch) | "CI that's a cinch. One config. Every forge. Your hardware." Go, self-hostable control plane, workers run jobs in containers | `cinch` in `~/.cinch/bin` via `curl -fsSL https://cinch.sh/install.sh \| sh` | v0.3.0 (Feb 2026), still served from `https://cinch.sh/dl/latest.json`; GitHub repo now 404s | **High** - same binary name and also Docker/self-hosting/devops audience. Only on PATH if user added `~/.cinch/bin`. |
| cinchcli.com (github.com/cinchcli) | "Your clipboard. Across every machine." Rust, AI-agent clipboard relay, self-hostable relay | `/usr/local/bin/cinch` (installer default `PREFIX=/usr/local`); also `brew install --cask cinchcli/tap/cinchcli` | Active (pushed 2026-10-25) | **High** - installs to the exact same path a `curl \| sh` installer for us would likely use; different domain. |
| Fedora / EPEL 7 `cinch` (RedHatQE/cinch) | "A tool for provisioning Jenkins components for CI" (Ansible) | `/usr/bin/cinch` (+ `teardown`) | In Fedora 39-43 and EPEL 7; rawhide has `dead.package` ("Orphaned for 6+ weeks"); upstream last push 2020 | **Medium** - real PATH clash on Fedora/RHEL hosts if installed, but niche and dying. |
| PyPI `cinch` (same RedHatQE project) | as above | console_scripts `cinch`, `teardown` | 1.4.0, last upload 2019 | Low - stale, opt-in `pip install`. |
| 00200200/cinch (GitHub, Python) | CLI to install agent skills across coding clients | `[project.scripts] cinch = "cinch.cli:main"`, dist name `cinch-init` | 35 stars, active; not on PyPI (`cinch-init` 404) | Low - install from git only. |
| npm `cinch` | "Async control flow made easy" | bin is `node-cinch`, not `cinch` | 0.1.1, 2022 | None. |
| Go: `github.com/go-cinch/cinch/cmd/cinch` | Go code generator (inferred from package names) | `go install` would produce `cinch` in `$GOBIN` | v1.2.1 (Feb 2025); GitHub repo 404s | Low - opt-in, obscure. |

Clear (no `cinch` package or binary):
- Debian (packages + sources): no results.
- Ubuntu packages: no results.
- Arch official repos: 0 results. AUR: 0 results.
- nixpkgs: no `pkgs/by-name/ci/cinch`; not listed on Repology for any Nix repo.
- Homebrew formula `cinch`: 404. Homebrew **cask** `cinch` exists (Irradiated Software's macOS window manager, `Cinch.app`, deprecated 2025-03-01 as unmaintained) - a GUI app, no CLI, so no PATH clash. It does mean `brew install --cask cinch` is taken and a future homebrew-core formula would share a token with that cask.
- crates.io `cinch`: "Redis-compatible cache infrastructure" library, 46 downloads - no binary.
- RubyGems `cinch`: IRC bot framework library (see below) - no binary.

## 2. Same-domain projects (self-hosting / Docker / devops)

1. **ehrlich-b/cinch (cinch.sh)** - self-hosted container CI. Owns the `cinch.sh` domain, which is the obvious vanity domain for a `cinch` CLI. Searches for "cinch self-hosted docker" surface it first (pkg.go.dev). Biggest discoverability/confusion risk.
2. **cinchcli** - self-hostable relay, AI/dev tooling audience; owns `cinchcli.com` and GitHub org `cinchcli`.
3. RedHatQE/cinch - CI/Jenkins provisioning, effectively dead.

## 3. Name-only (other domains)

- cinchrb/cinch - Ruby IRC bot framework, 999 stars, archived (last push 2019), ~1.4M gem downloads. Dominates GitHub search for "cinch".
- palantir/Cinch - Java MVC library, archived.
- Cinch (Irradiated Software) - macOS window manager (Homebrew cask).
- Cinch Audio Recorder - Windows consumer app; SEO repos crowd GitHub search.
- go-cinch org - Go microservice framework packages (`go-cinch/common`, `go-cinch/auth`).
- laristra/cinch - CMake build system (LANL).

## 4. Our namespaces

| Name | Result | Source |
|---|---|---|
| `github.com/abhinavrau/cinch` | Free (API 404) | `gh-axi api repos/abhinavrau/cinch` |
| Docker Hub `abhinavrau/cinch` | Free (404) | https://hub.docker.com/v2/repositories/abhinavrau/cinch/ |
| Docker Hub official `library/cinch` | Does not exist | https://hub.docker.com/v2/repositories/library/cinch/ |
| Docker Hub org `cinch` | **Taken** (created 2014-10-24, 0 repos) - `cinch/cinch` unavailable | https://hub.docker.com/v2/orgs/cinch |
| GHCR `ghcr.io/abhinavrau/cinch` | Not publicly present (anonymous pull token denied, same as any nonexistent/private repo; a known public image returns a token). Namespace is owned by abhinavrau, so it cannot be taken by others. | https://ghcr.io/token?scope=repository:abhinavrau/cinch:pull |
| GitHub user/org `cinch` | Not resolvable via API (404) | `gh-axi api users/cinch` |
| Domains `cinch.sh`, `cinch.dev` | Both registered and serving (cinch.sh = ehrlich-b CI) | https://cinch.sh |

## Risks for the maintainer to judge

1. A user who runs the cinchcli installer or cinch.sh CI installer gets a different `cinch` on PATH; ours would shadow or be shadowed depending on install dir order. Our installer/self-update should not blindly overwrite an existing non-ours `/usr/local/bin/cinch`.
2. cinch.sh is a same-audience (self-hosted, containers) tool with the same binary name - search and support confusion likely.
3. Fedora/RHEL hosts with the `cinch` RPM installed have `/usr/bin/cinch`.
4. Docker Hub `cinch/*` is unavailable; images must live under `abhinavrau/cinch` or GHCR.

## Sources

- Repology aggregate: https://repology.org/api/v1/project/cinch
- Fedora retirement: https://src.fedoraproject.org/rpms/cinch/raw/rawhide/f/dead.package
- RedHatQE entry points: https://github.com/RedHatQE/cinch/blob/master/setup.py
- PyPI: https://pypi.org/pypi/cinch/json
- Debian: https://packages.debian.org/search?keywords=cinch&searchon=names&suite=all&section=all , https://sources.debian.org/api/search/cinch/
- Ubuntu: https://packages.ubuntu.com/search?keywords=cinch&searchon=names&suite=all&section=all
- Arch: https://archlinux.org/packages/search/json/?name=cinch ; AUR: https://aur.archlinux.org/rpc/v5/search/cinch?by=name
- nixpkgs: https://github.com/NixOS/nixpkgs/tree/master/pkgs/by-name/ci (no `cinch`)
- Homebrew: https://formulae.brew.sh/api/formula/cinch.json (404), https://formulae.brew.sh/api/cask/cinch.json
- npm: https://registry.npmjs.org/cinch ; RubyGems: https://rubygems.org/api/v1/gems/cinch.json ; crates.io: https://crates.io/api/v1/crates/cinch
- Go: https://pkg.go.dev/search?q=cinch , https://pkg.go.dev/github.com/ehrlich-b/cinch , https://pkg.go.dev/github.com/go-cinch/cinch/cmd/cinch
- cinch.sh installer: https://cinch.sh/install.sh , https://cinch.sh/dl/latest.json
- cinchcli: https://cinchcli.com , https://cinchcli.com/install.sh , https://github.com/cinchcli/cinch
- 00200200/cinch: https://github.com/00200200/cinch/blob/main/pyproject.toml
- GitHub search: https://github.com/search?q=cinch+in%3Aname&type=repositories&s=stars
- Docker Hub: https://hub.docker.com/v2/orgs/cinch , https://hub.docker.com/v2/search/repositories/?query=cinch
