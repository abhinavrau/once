# Runtime Credential from the Environment

Amends [ADR-0006](0006-direct-minimal-scope-tailscale-credential.md): its setup
command line hands the secret to `--client-secret`, which this ADR removes.
Everything else in ADR-0006 stands, including which credential exists and what
scopes it holds.

`once tailscale enable` took the Runtime Credential
([ADR-0006](0006-direct-minimal-scope-tailscale-credential.md)) as
`--client-secret`. On Linux `/proc/<pid>/cmdline` is world-readable (mode
0444), so for the lifetime of the enable run any unprivileged local user could
read the secret, and it stayed behind in shell history and in any audit path
that captures argv. Since ADR-0006 that credential is the most powerful
Tailscale credential that ever exists - `auth_keys`, `devices:core`,
`services`, tagged - so the leak promotes any local account to minting tagged
auth keys on the tailnet.

We decided the secret arrives only through the environment:

- `ONCE_TAILSCALE_CLIENT_SECRET` is the sole input. `/proc/<pid>/environ` is
  mode 0400, readable by the process owner and root only, and root is already
  root. Every secret manager (`systemd-creds`, `pass`, 1Password, secretspec)
  delivers secrets as environment variables, so all of them work with no code
  in Once and none is named in the docs.
- `--client-secret` stays registered and always fails, naming the variable. A
  rejected flag gives an actionable error where deletion gives `unknown flag`,
  and unlike a warning (the `docker login --password` model) it actually closes
  the hole: a warning does not survive copy-paste from a blog post.
- `--client-id` is unchanged. It is an identifier, not a secret, so argv
  exposure of it grants nothing.
- Unset, or set-and-empty, is one clear error. There is no CLI prompt: the TUI
  enable form is the interactive path, and a second no-echo implementation
  would have to negotiate the terminal with `runShowingProgress`. The value is
  whitespace-trimmed, because a client secret never contains whitespace and
  piped values often carry a trailing newline.
- The variable is read in `internal/command`, never through
  `controlSeamActive()`. It shares the `ONCE_TAILSCALE_` prefix with the hidden
  Control Seam variables, so that prefix alone no longer marks a hidden seam.

Standard input was rejected as the channel despite leaving no trace in `/proc`
at all: it duplicates the TUI's interactive path, forces terminal ownership
rules on a command that renders progress, and composes with no secret manager.
A `--client-secret-file` flag is the environment variable plus a temporary file
to clean up and a permission mode to get wrong. Tailscale's own
`TS_CLIENT_SECRET` name was rejected because Once must not read a namespace it
does not own: a host already running Tailscale containers may have it set for a
different credential, and Once would silently take that value.

The explicit non-goal is protection at rest. The Runtime Credential stays
plaintext in the `once` label and in Docktail's container environment, because
Docktail must hold it to mint auth keys, and anyone who can read it through the
Docker socket can already `docker exec` anywhere on the host. Encrypting the
label would only move the problem to the key stored beside it. For the same
reason Once integrates no secret manager: it reads the credential once, at
enable, and copies it into Docker state, so central rotation changes nothing
about the copy that is already there.

## Consequences

- Automation that passes `--client-secret` breaks at the next upgrade,
  deliberately and with a message that says what to do instead. Once is pre-1.0
  and the flag appears in no README; `docs/research/tailscale-oauth-client-setup.md`
  carries the setup command line and moves to the variable.
- `docs/research/oauth-credential-handling.md` (2026-07-21) recommended keeping
  the flag and adding stdin, a prompt, and the variable beside it. Its at-rest
  reasoning is upheld here; its input recommendation is not, so it is marked as
  decided by this ADR rather than followed.
- The `enable` long help changes: its setup step 3 sets the variable rather
  than passing a flag, and its example reads the secret with `read -rs` rather
  than showing an inline `VAR=secret once ...` assignment. That form is not a
  leak this ADR closes - an inline assignment never reaches argv - but it does
  land in shell history, so the help should not teach it.
- The variable is resolved in the `enable` command's `PreRunE`, so a refused
  command line reports the refusal before `WithNamespace` touches Docker. In
  `RunE` it would surface as a Docker error on a host where Docker is down.
- Nothing in the existing suite goes through the flag - integration tests call
  `ns.EnableTailscale` directly and the TUI test fills form fields - so the
  rejection and the unset error need their own unit coverage in
  `internal/command`.
