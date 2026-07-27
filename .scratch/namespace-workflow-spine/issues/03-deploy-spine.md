# 03 - Deploy spine on Namespace

**What to build:** Deploying an Application from the CLI and installing one from
the TUI should run the same sequence, because today they run two independently
maintained copies of it and the copies have already diverged. Give Namespace a
Deploy entry point that owns the whole lifecycle, and reduce both frontends to
callers that supply Configuration and render the result.

The spine owns, in order: validate the Configuration, reject a Host already in
use, set up the namespace, generate the unique app name, construct the
Application, run the warn-only checks, deploy with progress, destroy the
part-built Application if the deploy fails, and verify it answers over HTTP.

It takes a fully built Configuration. Exposure and Host resolution stay with the
callers, because the TUI resolves Host interactively while the user types and
cannot defer it to submit time. The spine always assigns the app name itself,
since Deploy always creates a new Application.

Two things move into the engine as a consequence:

- Configuration validation. It currently runs only on the CLI path, called by
  hand in two places - once for deploy, once for update. Inside the spine it runs
  for every caller, and the deploy-path call site is deleted here. The
  update-path one goes with 04.
- The warn-only public DNS check, which lives in the CLI today and is why TUI
  users get no warning that a public Host does not resolve here. It returns as a
  warning rather than printing, alongside the existing Tailscale service-name
  shadow warning.

Warnings come back as a list beside the error, matching the shape
`EnableTailscale` already uses, and each frontend renders them its own way. This
changes CLI output ordering: warnings currently print before the progress
display and will print after it. That is accepted, and matches how Tailscale
enable already behaves.

The Docker client is still a concrete type at this point, so the spine's
container-touching steps remain covered by the integration suite. The precondition
ordering is the part most likely to break silently in a move and is unit-testable
without Docker, following the pattern already used for `EnableTailscale`.

**Blocked by:** 02 - One source of Application settings defaults.

**Status:** ready-for-agent

- [ ] Namespace exposes one Deploy entry point returning the Application, any warnings, and an error
- [ ] The CLI deploy command and the TUI install path both call it and contain no lifecycle ordering of their own
- [ ] Invalid Configuration is rejected on both paths, including from the TUI
- [ ] A public deploy from the TUI warns when the Host does not resolve to this machine, and never blocks on it
- [ ] A tailnet deploy still warns when the service name is shadowed by a tailnet machine
- [ ] A failed deploy destroys the part-built Application on both paths
- [ ] A deploy that starts but fails HTTP verification is removed, on both paths
- [ ] Download progress still reaches the CLI progress display and the TUI install screen
- [ ] Precondition ordering is unit-tested without a Docker daemon
- [ ] Integration tests pass, with CLI warning-ordering assertions updated
