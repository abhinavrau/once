# Creating the Tailscale OAuth client for `once tailscale enable`

Status: done (rewritten for ADR-0015's Tailnet Topology seam - the scopes and
the policy snippet now differ per topology. The previous revision described
only the Services topology; the revision before that described the ADR-0004
bootstrap flow, whose central claim proved wrong - see "Scope attenuation".)

Once uses one OAuth client, created directly in the Tailscale admin console
(ADR-0006, `docs/adr/0006-direct-minimal-scope-tailscale-credential.md`): a
tagged client that is the credential the topology's provider runs with, and
the most powerful Tailscale credential that ever exists in this design.

Which scopes it needs depends on the Tailnet Topology (ADR-0015,
`docs/adr/0015-tailnet-topology-seam-nodes-default.md`):

| Topology | `--topology` | Provider | Console scopes | API identifiers |
| --- | --- | --- | --- | --- |
| Nodes (default) | `nodes` | TSDProxy, one tailnet node per app | Keys > Auth Keys, Devices > Core (Write); Services (Read) | `auth_keys`, `devices:core`, `services:read` |
| Services | `services` | Docktail, one shared node advertising a Service per app | Keys > Auth Keys, Devices > Core, Services (all Write) | `auth_keys`, `devices:core`, `services` |

Both are tagged clients carrying your tag (default `tag:once`). The scope
lists are not prose in this doc alone - they are `TopologyFacts.OAuthScopes`
in `internal/docker/tailscale_nodes.go` and
`internal/docker/tailscale_services.go`, which is what the CLI help and the
enable form render.

A fresh `once tailscale enable` with no `--topology` resolves to **nodes**; an
already-enabled install keeps its stored topology. Switching topology later
means a new policy snippet and (for nodes -> services) a new credential with
`services` write, since scopes are fixed at creation.

## What the code needs, either topology

```sh
read -rs ONCE_TAILSCALE_CLIENT_SECRET && export ONCE_TAILSCALE_CLIENT_SECRET
once tailscale enable --client-id <id> [--topology nodes|services] [--tag tag:once]
```

The secret arrives only in the environment; `--client-secret` is refused
(ADR-0013). `read -rs` keeps it out of shell history too, which an inline
`VAR=... once ...` would not. The TUI form takes the same values ("Client ID",
"Client Secret", "Tag" - `internal/ui/tailscale_form.go`). `--tag` defaults to
`tag:once`; bare names get `tag:` prepended and comma-separated lists are
accepted (`internal/docker/tailscale.go` `normalizeTag`).

Before booting any container, Once probes the credential with the same request
shapes the topology's provider uses at runtime (below). Each failure mode gets
its own actionable error; a missing-tag failure carries
`docker.ErrTagNotInPolicy` and the CLI prints the policy snippet after it.

The hidden headscale control seam (integration tests) skips OAuth entirely.

## Step 1: merge the policy snippet - per topology

A tag must exist in the `tagOwners` section of the tailnet policy before
anything can carry it ([kb/1068](https://tailscale.com/kb/1068/tags)), and the
admin console's credential form only offers tags that already exist - so the
policy edit comes first, either topology.

```sh
once tailscale policy                        # Nodes, the default
once tailscale policy --topology services    # Services
```

`policy` and `enable` default to the same topology (nodes), so a fresh install
needs no `--topology` on either. On a Services install, pass
`--topology services` or you will merge the wrong snippet.

Merge the printed HuJSON into <https://login.tailscale.com/admin/acls>, or
commit it to the policy repo if the policy is GitOps-managed.

**Nodes** (`NodesPolicySnippet`) - two sections:

- `tagOwners` - defines the tag (owner: `autogroup:admin`), so the client may
  create devices with it.
- the `funnel` nodeAttr - for `once tailscale funnel enable`
  ([kb/1223](https://tailscale.com/kb/1223/funnel)).

No `autoApprovers.services` and no grant: a tagged node is an ordinary tailnet
device, reachable under the tailnet's existing default/ACL rules like any
other, with no approval or access-grant step of its own.

**Services** (`PolicySnippet`) - the same two, plus two more:

- `autoApprovers.services` - so new app Services don't sit pending approval
  ([kb/1552](https://tailscale.com/kb/1552/tailscale-services)).
- a grant from `autogroup:member` to the tag on port 443 - Services access.
  The dst is the tag, not `svc:` names, so one grant covers apps deployed
  later.

Both live in `internal/docker/tailscale_policy.go`; the enable-time credential
probe is the enforcement.

## Step 2: create the credential in the admin console

You need an Owner or Admin account, or a user with permissions for these
scopes and the tag ([kb/1215](https://tailscale.com/kb/1215/oauth-clients)).

1. Open the **Trust credentials** page:
   <https://login.tailscale.com/admin/settings/trust-credentials>
   (Settings -> Trust credentials; this page replaced the old OAuth clients
   page - [kb/1215](https://tailscale.com/kb/1215/oauth-clients),
   [kb/1623](https://tailscale.com/kb/1623/trust-credentials)).
2. Select the **Credential** button, then **OAuth**.
3. Select **Write** on exactly the scopes for your topology (console label ->
   API identifier, per the
   [trust-credentials reference](https://tailscale.com/docs/reference/trust-credentials)):
   - **Keys > Auth Keys** -> `auth_keys` (both topologies)
   - **Devices > Core** -> `devices:core` (both topologies)
   - **Services** -> `services` (**Write** for Services, **Read** for Nodes)

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
once tailscale enable --client-id <id>              # nodes, the default
once tailscale enable --client-id <id> --topology services
```

The probe runs first; on success the credential is stored in the `once`
container label and the topology's containers boot.

## What each scope is for, and what the probe does with it

Per the endpoint annotations in the
[OpenAPI spec](https://api.tailscale.com/api/v2?outputOpenapiSchema=true).
Both topologies first exchange the credential for a token at
`POST /api/v2/oauth/token` (`internal/docker/tailscale_api.go`); rejected
credentials surface there, before any key is minted.

### Nodes (`validateNodesCredentials`, `internal/docker/tailscale_nodes.go`)

- `auth_keys` - TSDProxy mints one auth key per app node at runtime. The probe
  mints and deletes a key with TSDProxy's exact shape: non-reusable (one key
  per node), non-ephemeral (app nodes keep tsnet state on the data volume),
  preauthorized, tagged (`POST`/`DELETE /tailnet/-/keys`). Keys minted by a
  tagged OAuth client "must have those exact tags, or tags owned by the
  credential's tags"
  ([trust-credentials reference](https://tailscale.com/docs/reference/trust-credentials)),
  which is why the credential carries the same tag Once mints keys for.
- `devices:core` - `GET /tailnet/-/devices`, used by the deploy-time
  host-collision check (so TSDProxy never falls back to a `-1` alias) and by
  the best-effort deletion of per-app devices on teardown.

- `services:read` - `GET /tailnet/-/services/svc:{name}` at deploy time.
  Nodes creates no Services, but a Service holds a Magic DNS name just like a
  device, and one left by the services topology turns the app's node into
  `{name}-1`. The deploy is refused instead, naming the Service. Not probed
  at enable: without the scope the check is skipped, so a credential created
  before this requirement keeps working.

### Services (`validateOAuthCredentials`, `internal/docker/tailscale_services.go`)

- `auth_keys` - mint the shared sidecar's tagged, preauthorized, **reusable**
  key (`POST`/`DELETE /tailnet/-/keys`).
- `services` - create/update/delete Service definitions
  (`GET`/`PUT`/`DELETE /tailnet/-/services/{name}`), one per exposed app. The
  probe creates, inspects, and deletes a throwaway `svc:once-probe` Service
  with a payload byte-compatible with Docktail's own.
- `devices:core` - required *together with* `services` to list which devices
  host a Service (`GET /tailnet/-/services/{name}/devices` is annotated
  "OAuth Scope: `services`, `devices:core`").

#### Services + `--mode host`

Host mode (ADR-0007) needs no extra OAuth scope, but adds four preflight
checks on top of the credential probe (`validateHostModePreflight`):
a running host `tailscaled`, the host node carrying your tag
(`sudo tailscale up --advertise-tags=tag:once`), host daemon v1.86.0+, and the
host node present in the credential's tailnet - that last one reading the
devices list, so `devices:core` covers it. Sidecar mode is the default and
needs none of this.

## Scope attenuation (why the bootstrap design died)

An earlier revision, following ADR-0004, claimed a single `oauth_keys`-scoped
credential could mint the client above, because the create-key endpoint's
description says the returned key "wields the capabilities or scopes specified
in the request". In practice the API attenuates: an OAuth client creating
another OAuth client cannot grant scopes it does not itself hold, and rejects
with HTTP 403 `actor cannot set scopes: [...]` (observed live on 2026-07-19
against `POST /api/v2/tailnet/-/keys`; consistent with kb/1215's rule that
non-admin creators are limited to "the scopes and tags for which they have
permissions"). A bootstrap credential would need every scope it delegates,
making it strictly more powerful than the credential it produces - so ADR-0006
drops the indirection.

## Gotchas

- **`policy` does not know your install.** It reads no Docker state, so it
  cannot see a stored topology; it always prints the default (nodes) unless you
  pass `--topology services`. On a Services install, pass it.
- **Copy the secret immediately.** It is displayed exactly once on the
  Credential created page ([kb/1215](https://tailscale.com/kb/1215/oauth-clients)).
- **Scopes are fixed at creation.** To change scopes - including moving a nodes
  install to services, which needs `services` write - create a new credential and
  revoke the old one on the Trust credentials page
  ([kb/1623](https://tailscale.com/kb/1623/trust-credentials)).
- **Policy first, credential second.** The console's tag picker only offers
  tags already in `tagOwners`; if your tag is missing, merge the snippet from
  `once tailscale policy` first.
- **Interrupted probes (Services).** A leftover `svc:once-probe` Service is
  recognized by its comment and reused by the next probe, so it never blocks a
  re-run. A different Service already holding that name fails the probe with a
  named error.
- **Plan/version floor (Services only).** Tailscale Services is GA on all plans
  with a limit of 10 Services per tailnet
  ([services-ga blog](https://tailscale.com/blog/services-ga)); Service hosts
  need Tailscale v1.86.0+ and a tag-based identity
  ([kb/1552](https://tailscale.com/kb/1552/tailscale-services)) - Once's pinned
  sidecar image and minted tagged key satisfy both. Nodes has no such ceiling:
  each app is an ordinary device, subject only to the tailnet's device limit.
- **A pre-ADR-0015 install has no stored topology** and reports
  `docker.ErrOlderOnceSetup`; re-running `once tailscale enable` rebuilds it as
  nodes unless `--topology services` is given. An existing Services install's
  credential already covers nodes' scopes, so no new credential is needed in
  that direction.
- **Leaving services deletes Once's Services.** Teardown, `disable`, and a
  switch to nodes best-effort delete every Service carrying Once's tag
  (`servicesTopology.leave`), through the Services credential's `services`
  write, so none keeps holding an app's name. A rebuild that stays on
  services keeps them.

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
  `auth_keys` on key minting, `devices:core` on the tailnet devices list.
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
- ADRs: `0015-tailnet-topology-seam-nodes-default.md`,
  `0006-direct-minimal-scope-tailscale-credential.md`,
  `0013-runtime-credential-from-the-environment.md`,
  `0007-sidecar-default-tailscale-mode.md`, `0002-docktail-tailscale-services.md`.
- Code: `internal/command/tailscale.go`, `internal/docker/tailscale_topology.go`
  (`TopologyFacts`), `internal/docker/tailscale_nodes.go`
  (`validateNodesCredentials`), `internal/docker/tailscale_services.go`
  (`validateOAuthCredentials`, `validateHostModePreflight`),
  `internal/docker/tailscale_policy.go`, `internal/ui/tailscale_form.go`.
