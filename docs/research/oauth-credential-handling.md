# OAuth Credential Handling for `once tailscale enable`

Status: research note, 2026-07-21. Question: best-practice way to (a) pass the
Tailscale OAuth client secret to the `once` CLI and (b) store it at rest.

Decided by ADR-0013, which followed this note on (b) and narrowed it on (a):
the environment variable is the only input, `--client-secret` is refused rather
than kept for scripts, and neither stdin nor a TTY prompt was added. Read the
recommendation below as the option space, not the outcome.

Prior context reused from local deja index: sessions `1602db36-1a7` (ADR-0006
direct minimal-scope credential work) and `cb66492f-200` (UX map) located the
current credential paths and the ADR; see
`docs/adr/0006-direct-minimal-scope-tailscale-credential.md`.

## TL;DR recommendation

1. Input: keep `--client-secret` for scripts, but add non-argv paths and prefer
   them: read from stdin (`--client-secret -` or `--client-secret-stdin`), an
   env var (`ONCE_TAILSCALE_CLIENT_SECRET`), and an interactive hidden prompt
   when the flag is omitted on a TTY. This is exactly the `docker login
   --password-stdin` / `gh auth login --with-token` pattern. Flags leak via
   shell history and world-readable `/proc/<pid>/cmdline`.
2. At rest: keep the Docker-label storage, deliberately. On a headless server
   there is no OS keyring (zalando/go-keyring needs the D-Bus Secret Service),
   an encrypted file whose key sits next to it is obfuscation (OWASP 7.3), and
   the secret must be handed to the once-docktail container as env anyway - so
   anyone who can read the label (docker-socket users, who Docker says must be
   trusted like root) can already read the env. Storing it anywhere "more
   secure" than the container it configures adds no real boundary. Document
   this trust model, and guard the leak paths instead: never print the secret
   (status/json/logs/progress), keep the TUI field masked, and note that
   `docker inspect` output and `/var/lib/docker` backups contain it.

## Current state in once

Input today:

- CLI: required flags only. `--client-id` and `--client-secret` on
  `once tailscale enable`; both `MarkFlagRequired`
  (`internal/command/tailscale.go:47-52`). No env var, stdin, or prompt
  fallback. Values pass straight into `docker.TailscaleSettings`
  (`internal/command/tailscale.go:106-108`).
- TUI: "Client Secret" text field, masked via `SetEchoPassword()`
  (`internal/ui/tailscale_form.go:248-250`, mask impl
  `internal/ui/form.go:74-76`). The form prefills the stored secret with
  `SetValue(settings.ClientSecret)` (`internal/ui/tailscale_form.go:250`),
  loaded via `Tailscale.LoadSettings` (`internal/docker/tailscale.go:289-294`).

Storage today:

- `TailscaleSettings.ClientSecret` has a plain `json:"clientSecret"` tag
  (`internal/docker/tailscale.go:110-112`) and the whole struct is marshalled
  into the `once` label (`labelKey = "once"`, `internal/docker/proxy.go:23`) on
  the `once-docktail` container (`internal/docker/tailscale.go:571-572`), read
  back at `internal/docker/tailscale.go:208,294,428,448`.
- The secret is also injected into the docktail container as
  `TAILSCALE_OAUTH_CLIENT_SECRET` env (`internal/docker/tailscale.go:548-549`).
- The tailscale sidecar does NOT get the OAuth secret; it gets a one-shot
  minted auth key as `TS_AUTHKEY` (`internal/docker/tailscale.go:491,505`).
- The stored secret is re-read and sent to `api.tailscale.com` for token
  exchange during status/verify flows
  (`internal/docker/tailscale_status.go:39`,
  `internal/docker/tailscale_validate.go:218`).

## Input mechanisms compared

| Mechanism | Leak surface | Verdict |
|---|---|---|
| Flag (`--client-secret X`) | Shell history; `/proc/<pid>/cmdline`, which under the default `hidepid=0` "everybody may access all /proc/pid directories" (proc(5), hidepid: https://man7.org/linux/man-pages/man5/proc.5.html) | Keep only for scripted use; steer docs away from it |
| Env var | Not in argv/history. `/proc/<pid>/environ` is gated by a `PTRACE_MODE_READ_FSCREDS` check (https://man7.org/linux/man-pages/man5/proc_pid_environ.5.html), i.e. same-uid/root only - stronger than cmdline, weaker than nothing: env propagates to child processes and "may be included in logs or system dumps" (OWASP Secrets Management Cheat Sheet 5.1, https://cheatsheetseries.owasp.org/cheatsheets/Secrets_Management_Cheat_Sheet.html) | Good headless default; gh does this with `GH_TOKEN` ("most suitable for 'headless' use", https://cli.github.com/manual/gh_auth_login) |
| Stdin | No argv, no env, no history: docker docs: "Using STDIN prevents the password from ending up in the shell's history, or log-files" (https://docs.docker.com/reference/cli/docker/login/) | Best scripted path; the `docker login --password-stdin` / `gh auth login --with-token` standard |
| Interactive prompt (hidden) | Nothing persisted | Best human path; docker login's default when no password flag given (same docker login doc) |
| File path | 0600 file; used by kamal-style `.kamal/secrets` command substitution (https://kamal-deploy.org/docs/commands/secrets/) and Docker secrets' `*_FILE` convention (https://docs.docker.com/engine/swarm/secrets/) | Nice-to-have, not required |

RFC 6749 defines once as a confidential-client holder: client credentials must
stay confidential and be sent only over TLS (secs 2.3.1, 10.1,
https://datatracker.ietf.org/doc/html/rfc6749). RFC 8628 (device grant,
https://datatracker.ietf.org/doc/html/rfc8628) is for browserless user
authorization of end users on TVs/printers etc - not applicable here: this is a
machine credential, no user-delegated authorization is happening, and Tailscale
issues static OAuth client secrets, not device-flow grants.

## Storage-at-rest options compared

- OS keyring: zalando/go-keyring "depends on the Secret Service dbus
  interface, which is provided by GNOME Keyring"
  (https://github.com/zalando/go-keyring) - absent on the headless servers once
  targets, so Set/Get just error. gh wraps it with a 60s timeout for hung
  keyrings (https://github.com/cli/cli/blob/trunk/internal/keyring/keyring.go)
  and offers plaintext fallback via `--insecure-storage` ("Save authentication
  credentials in plain text instead of credential store",
  https://cli.github.com/manual/gh_auth_login). 99designs/keyring adds headless
  backends (pass, encrypted file, keyctl) (https://github.com/99designs/keyring)
  but pass/keyctl need per-host setup and the encrypted file needs a passphrase
  - a prompt on every daemonless invocation. Not viable as a default here.
- Encrypted file (age / NaCl secretbox): only moves the problem to key
  storage. OWASP 7.3: "You should not store keys next to the secrets they
  encrypt" (cheat sheet above). once has no other place for the key, and the
  secret must still be delivered in cleartext to docktail's env.
- Plaintext file, 0600: the actual floor for major CLIs. AWS CLI
  `~/.aws/credentials`: "These files are plaintext files"
  (https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-files.html).
  Docker without a helper "stores credentials in the config.json file in a
  base64-encoded format. This method is less secure than configuring and using
  a credential store" (docker login doc above). gh falls back to plain text per
  above. Workable for once, but it would be the first once state living
  outside Docker, breaking the "everything in Docker state" design (AGENTS.md).
- Docker label (current): "A label is a key-value pair, stored as a string";
  static for the container's lifetime, viewable via `docker inspect`
  (https://docs.docker.com/engine/manage-resources/labels/). Readable by anyone
  with daemon access - and Docker's own security docs say "only trusted users
  should be allowed to control your Docker daemon" because daemon access
  enables trivial privilege escalation
  (https://docs.docker.com/engine/security/). Key nuance: that same audience
  can read the docktail container's env (`docker inspect` shows `Config.Env`),
  which must contain the secret for docktail to work
  (`internal/docker/tailscale.go:548-549`). So the label adds no NEW readers
  beyond what the architecture already requires. Residual risks: `docker
  inspect` output pasted into logs/issues, and `/var/lib/docker` backups.
- Docker secrets (swarm): "only available to swarm services, not to standalone
  containers" (https://docs.docker.com/engine/swarm/secrets/) - once runs
  standalone containers, so unavailable. (Their design notes do endorse
  file-over-env: "environment variables can unintentionally be leaked".)
- Compose-style file secret: outside swarm, Compose "secrets" have no engine
  store at all - the secret is a host file mounted into the container at
  `/run/secrets/<name>` (https://docs.docker.com/compose/how-tos/use-secrets/).
  once drives the Engine API directly and can replicate this with a plain bind
  mount, no Compose adoption needed. docktail already supports the receiving
  end: `TAILSCALE_OAUTH_CLIENT_SECRET_FILE` and `FILE__TAILSCALE_OAUTH_CLIENT_SECRET`,
  "useful with Docker secrets, Swarm secrets, and other secret mounts"; direct
  env takes precedence, then `FILE__` prefix, then `_FILE` suffix
  (https://docktail.org/docs/#environment-variables, full text at
  https://docktail.org/llms-full.txt). What it takes: write the secret to a
  0600 host file on enable, bind-mount it into once-docktail, set the `_FILE`
  env instead of the value, and drop `ClientSecret` from the settings label
  (else no gain). Cost: first once state outside Docker labels (breaks the
  "everything in Docker state" design), plus lifecycle plumbing - delete on
  disable/teardown, re-enable and the enabled-overlay settings display read the
  file, handle file-missing drift. Gain: secret leaves `docker inspect` output
  (both label and `Config.Env`) - real against pasted inspect output, label
  scrapers, and container-config backups, but no change to the core reader set
  (docker-socket users are root-equivalent and can read the host file).

## What comparable CLIs do

- gh: token via stdin (`--with-token`), `GH_TOKEN` env for headless, OS
  keyring (zalando/go-keyring, 60s timeout wrapper) with explicit plaintext
  opt-out `--insecure-storage` (https://cli.github.com/manual/gh_auth_login,
  https://github.com/cli/cli/blob/trunk/internal/keyring/keyring.go).
- docker login: interactive prompt default, `--password-stdin` recommended,
  base64-in-config.json fallback with a documented "less secure" warning,
  pluggable credential helpers (secretservice, pass, osxkeychain)
  (https://docs.docker.com/reference/cli/docker/login/).
- kamal (already in this stack): stores nothing; `.kamal/secrets` resolves
  values at deploy time via command substitution against password-manager
  adapters (1Password, Bitwarden, AWS/GCP Secret Manager, ...)
  (https://kamal-deploy.org/docs/commands/secrets/).
- aws: plaintext `~/.aws/credentials` profiles
  (https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-files.html).
- Tailscale itself: the client secret is shown once ("you won't be able to
  copy the secret again") and the only storage guidance is "Store the client
  secret securely" (https://tailscale.com/kb/1215/oauth-clients). Their Docker
  image takes credentials via env (`TS_AUTHKEY` etc,
  https://tailscale.com/kb/1282/docker) - i.e. Tailscale's own container
  guidance accepts env-visible-in-inspect as the operating model.

## Concrete recommendation for once

Input (do this):

1. Make `--client-secret` optional; resolution order: flag, then
   `ONCE_TAILSCALE_CLIENT_SECRET` env, then `--client-secret -` / stdin, then
   hidden interactive prompt on a TTY. Error only when none is available.
2. Document stdin/env as the blessed non-interactive paths; mention in the
   flag help that argv is visible in shell history and process listings.
3. TUI already masks the field (`internal/ui/tailscale_form.go:249`); keep it.

At rest (decide, then document):

4. Keep the `once` label on `once-docktail` as the store. Rationale: the
   readers of the label (docker-socket users, root-equivalent per Docker
   security docs) are exactly the readers of the env var docktail cannot run
   without; a headless server offers no keyring; an encrypted file's key would
   sit beside it (OWASP 7.3); Docker secrets need swarm. Moving the secret to
   a 0600 file matches gh/aws precedent but splits once's state model for zero
   reduction in the actual reader set. Record this as a short ADR so the
   trade-off is explicit.
5. Enforce redaction at the seams: `once tailscale status --json` and any
   progress/verbose output must never include `ClientSecret` (ADR-0005 already
   commits verbose logging to metadata-only); add a unit test asserting the
   status JSON and printed output do not contain the stored secret.
6. Optional hardening: the Compose-style file secret above. docktail already
   supports `TAILSCALE_OAUTH_CLIENT_SECRET_FILE`, so the mechanics are small; a
   0600 host file bind-mounted into once-docktail removes the secret from
   `docker inspect` output entirely. The deliberate trade is once's first piece
   of state outside Docker labels - if adopted, record that break in the same
   ADR as item 4.
