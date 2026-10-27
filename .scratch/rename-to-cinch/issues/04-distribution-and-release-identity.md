# Distribution and release identity

Status: done
Assignee: arau
Labels: wayfinder:grilling
Blocked by: (none)

## Question

How does cinch reach a machine and update itself?

- The installer (installer/, deployed to Basecamp's `get.once.com`): the
  project owns `cinch.host`. Serve the install script from it (e.g.
  `get.cinch.host` or `cinch.host/install`), and how is it hosted (keep the
  Kamal deploy in installer/config/deploy.yml, or a static file)?
- Versioning: restart at v0.1.0 or continue from upstream's v0.1.x line?
- Rename the GitHub repo `abhinavrau/once` -> `abhinavrau/cinch` (GitHub
  redirects the old URL) or create a new repo?
- The kamal-proxy fork image tag `abhinavrau/kamal-proxy:once-02`: rename it,
  or leave it as it is?
- Two third-party CLIs already install a `cinch` binary (see ticket 01).
  How do the installer and self-update avoid overwriting a `cinch` that
  isn't ours?

## Comments

Resolved (grilling, 2026-10-27):

1. Install URL: `curl https://get.cinch.host | sh`.
2. Installer hosting: one static `install.sh` served at `get.cinch.host`
   (GitHub Pages or Cloudflare; the exact host is an execution detail).
   Delete the Go server (installer/main.go, Dockerfile, config/deploy.yml).
   Per-image paths (`get.once.com/<image>`) become an argument:
   `curl https://get.cinch.host | sh -s -- <image>`.
3. Foreign `cinch` on PATH: the installer reuses an existing `cinch` only if
   `cinch --version` carries our marker; otherwise it refuses, names the
   path, and never overwrites it. Self-update only replaces
   `os.Executable()` and needs no guard.
4. Versioning: the first cinch release is `v0.2.0`, above every upstream
   `v0.1.x` tag, so `git describe` and the self-update semver compare never
   go backwards. Upstream tags stay as history.
5. GitHub: rename `abhinavrau/once` -> `abhinavrau/cinch` (keeps refs, old URL
   redirects). Module path `github.com/abhinavrau/cinch`. Self-update reads
   `api.github.com/repos/abhinavrau/cinch/releases/latest`; release assets
   are `cinch-<os>-<arch>`.
6. Detach the repo from basecamp/once's fork network via GitHub support.
7. Proxy image: retag the same digest as `abhinavrau/kamal-proxy:cinch-02`
   (no rebuild) and pin that in internal/docker/proxy.go.
