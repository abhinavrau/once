# Distribution and release identity

Status: open
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
