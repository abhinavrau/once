# Creating the Tailscale OAuth client for `once tailscale enable`

Status: done (rewritten for ADR-0006; the previous revision described the
ADR-0004 bootstrap flow, whose central claim proved wrong - see "Scope
attenuation" below)

Once uses one OAuth client, which you create directly in the Tailscale admin
console (ADR-0006, `docs/adr/0006-direct-minimal-scope-tailscale-credential.md`):
a tagged client with write access to Auth Keys, Devices Core, and Services.
It is the credential Docktail and the `once-tailscale` sidecar run with, and
the most powerful Tailscale credential that ever exists in this design.

## What the code needs

The enable flow (`internal/command/tailscale.go`) takes:

```sh
read -rs ONCE_TAILSCALE_CLIENT_SECRET && export ONCE_TAILSCALE_CLIENT_SECRET
once tailscale enable --client-id <id> [--tag tag:once]
```

The secret arrives only in the environment; `--client-secret` is refused
(ADR-0013). Reading it with `read -rs` keeps it out of shell history too, which
an inline `VAR=... once ...` would not.

or the same three values in the TUI form ("Client ID", "Client Secret", "Tag" -
`internal/ui/tailscale_form.go`). `--tag` defaults to `tag:once`; bare names
get `tag:` prepended and comma-separated lists are accepted
(`internal/docker/tailscale.go` `normalizeTag`).

Before booting any container, Once probes the credential with the same request
shapes Docktail and the sidecar use at runtime
(`internal/docker/tailscale_validate.go`): a token exchange at
`POST /api/v2/oauth/token`, mint-and-delete of a tagged auth key
(`POST`/`DELETE /api/v2/tailnet/-/keys`), then create, inspect, and delete a
throwaway Service `svc:once-probe`
(`GET`/`PUT`/`DELETE /api/v2/tailnet/-/services/{name}` and `GET .../devices`).
Each failure mode gets its own actionable error; a missing-tag failure carries
`docker.ErrTagNotInPolicy` and the CLI prints the policy snippet after it.

The hidden headscale control seam (integration tests) uses a pre-minted auth
key and skips OAuth entirely.

## Step 1: merge the policy snippet

A tag must be defined in the `tagOwners` section of the tailnet policy file
before anything can carry it ([kb/1068](https://tailscale.com/kb/1068/tags)),
and the admin console's credential form only offers tags that already exist -
so the policy edit must come first.

Run `once tailscale policy` (add `--tag` if you use a custom tag) and merge the
printed HuJSON snippet into <https://login.tailscale.com/admin/acls>. It covers
the four sections Once needs (`internal/docker/tailscale_policy.go`
`PolicySnippet`):

- `tagOwners` - defines the tag (owner: `autogroup:admin`).
- `autoApprovers.services` - so new app Services don't sit pending approval
  ([kb/1552](https://tailscale.com/kb/1552/tailscale-services)).
- a grant from `autogroup:member` to the tag on port 443 - Services access.
- the `funnel` nodeAttr - for `once tailscale funnel enable`
  ([kb/1223](https://tailscale.com/kb/1223/funnel)).

If the policy is managed via GitOps, commit the snippet to the policy repo
instead of pasting it into the console editor.

## Step 2: create the credential in the admin console

You need an Owner or Admin account, or a user with permissions for these
scopes and the tag ([kb/1215](https://tailscale.com/kb/1215/oauth-clients)).

1. Open the **Trust credentials** page:
   <https://login.tailscale.com/admin/settings/trust-credentials>
   (Settings -> Trust credentials; this page replaced the old OAuth clients
   page - [kb/1215](https://tailscale.com/kb/1215/oauth-clients),
   [kb/1623](https://tailscale.com/kb/1623/trust-credentials)).
2. Select the **Credential** button, then **OAuth**.
3. Select **Write** on exactly these scopes (console label -> API identifier,
   per the [trust-credentials reference](https://tailscale.com/docs/reference/trust-credentials)):
   - **Keys > Auth Keys** -> `auth_keys`
   - **Devices > Core** -> `devices:core`
   - **Services** -> `services`
   The `auth_keys` and `devices:core` write scopes force a tag selection
   ([kb/1215](https://tailscale.com/kb/1215/oauth-clients)); pick your tag
   (default `tag:once`). Optionally add a description like `once`.
4. Select **Generate credential** and copy both values:
   - **Client ID** (short, e.g. `k123456CNTRL`; not secret)
   - **Client secret**, of the form `tskey-client-k123456CNTRL-abcdef...`
     (example format from the
     [Kubernetes operator guide](https://tailscale.com/docs/kubernetes-operator/install-operator)).
   The secret is shown only once - "After you close the Credential created
   page, you won't be able to copy the secret again" - and it is case-sensitive
   ([kb/1215](https://tailscale.com/kb/1215/oauth-clients)).

## Step 3: enable

```sh
read -rs ONCE_TAILSCALE_CLIENT_SECRET && export ONCE_TAILSCALE_CLIENT_SECRET
once tailscale enable --client-id <id>
```

The probe runs first; on success the credential is stored in the `once`
container label and the Docktail topology boots.

## What each scope is for

Per the endpoint annotations in the
[OpenAPI spec](https://api.tailscale.com/api/v2?outputOpenapiSchema=true):

- `auth_keys` - mint the sidecar's tagged, preauthorized auth key
  (`POST /tailnet/{t}/keys`). Keys minted by a tagged OAuth client "must have
  those exact tags, or tags owned by the credential's tags"
  ([trust-credentials reference](https://tailscale.com/docs/reference/trust-credentials)),
  which is why the credential carries the same tag Once mints keys for.
- `services` - create/update/delete Tailscale Service definitions
  (`PUT`/`DELETE /tailnet/{t}/services/{name}`), one per exposed app.
- `devices:core` - required together with `services` to list which devices
  host a Service (`GET /tailnet/{t}/services/{name}/devices` is annotated
  "OAuth Scope: `services`, `devices:core`").

## Scope attenuation (why the bootstrap design died)

The previous revision of this doc, following ADR-0004, claimed a single
`oauth_keys`-scoped credential could mint the client above, because the
create-key endpoint's description says the returned key "wields the
capabilities or scopes specified in the request". In practice the API
attenuates: an OAuth client creating another OAuth client cannot grant scopes
it does not itself hold, and rejects with HTTP 403 `actor cannot set scopes:
[...]` (observed live on 2026-07-19 against `POST /api/v2/tailnet/-/keys`;
consistent with kb/1215's rule that non-admin creators are limited to "the
scopes and tags for which they have permissions"). A bootstrap credential
would need every scope it delegates, making it strictly more powerful than
the credential it produces - so ADR-0006 drops the indirection.

## Gotchas

- **Copy the secret immediately.** It is displayed exactly once on the
  Credential created page ([kb/1215](https://tailscale.com/kb/1215/oauth-clients)).
- **Scopes are fixed at creation.** To change scopes you create a new
  credential and revoke the old one on the Trust credentials page
  ([kb/1623](https://tailscale.com/kb/1623/trust-credentials)).
- **Policy first, credential second.** The console's tag picker only offers
  tags already in `tagOwners`; if your tag is missing, merge the snippet from
  `once tailscale policy` first.
- **Interrupted probes.** A leftover `svc:once-probe` Service is recognized
  (by its comment) and reused by the next probe, so it never blocks a re-run.
- **Plan/version floor.** Tailscale Services is GA on all plans with a limit of
  10 Services per tailnet
  ([services-ga blog](https://tailscale.com/blog/services-ga)); Service hosts
  need Tailscale v1.86.0+ and a tag-based identity
  ([kb/1552](https://tailscale.com/kb/1552/tailscale-services)) - Once's pinned
  sidecar image and minted tagged key satisfy both.

## Sources

- <https://tailscale.com/kb/1215/oauth-clients> - Trust credentials page URL and
  creation steps (Credential -> OAuth -> Generate credential), roles and
  per-user scope/tag limits, Read/Write scope selection, tags required for
  `auth_keys`/`devices:core`/`all`, secret shown once and case-sensitive.
- <https://tailscale.com/kb/1623/trust-credentials> - Trust credentials admin
  page, Revoke action, `devices:core` tag requirement.
- <https://tailscale.com/docs/reference/trust-credentials> - scope identifiers
  and descriptions, minted-key tag rule, companion-scope notes.
- <https://api.tailscale.com/api/v2?outputOpenapiSchema=true> - per-endpoint
  scope annotations: `services` + `devices:core` on the Service device listing,
  `auth_keys` on key minting.
- Live API behavior, 2026-07-19: `POST /api/v2/tailnet/-/keys` with
  `keyType: client` returns HTTP 403 `actor cannot set scopes: [...]` when the
  requested scopes exceed the creating client's own.
- <https://tailscale.com/docs/kubernetes-operator/install-operator> - console
  scope wording ("Keys / Auth Keys", "Devices / Core", "General / Services")
  and the `tskey-client-k123456CNTRL-abcdef` secret example.
- <https://tailscale.com/kb/1068/tags> - tags must be defined in `tagOwners`
  before use; policy file location in the admin console.
- <https://tailscale.com/kb/1552/tailscale-services> - tagged host requirement,
  v1.86.0+, `autoApprovers.services`, grants for Services.
- <https://tailscale.com/kb/1223/funnel> - `funnel` nodeAttr prerequisite.
- <https://tailscale.com/blog/services-ga> - GA on all plans, 10-Service limit.
- `docs/adr/0006-direct-minimal-scope-tailscale-credential.md`,
  `docs/adr/0002-docktail-tailscale-services.md`.
- `internal/command/tailscale.go`, `internal/docker/tailscale_validate.go`,
  `internal/docker/tailscale_policy.go`, `internal/ui/tailscale_form.go`.
