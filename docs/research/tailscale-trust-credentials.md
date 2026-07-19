# Creating the Tailscale Trust credentials for `once tailscale enable`

Once's Tailscale integration (ADR-0002) deploys a Docktail container that
publishes each exposed app as a native Tailscale Service, and (in sidecar mode)
boots a `once-tailscale` node whose auth key is minted at enable time. Both need
a Tailscale OAuth client. In the Tailscale admin console these now live on the
**Trust credentials** page (Settings -> Trust credentials), which replaced the
old OAuth clients page; "trust credentials" covers OAuth clients and federated
identities, and the OAuth kind is what Once uses
([kb/1215](https://tailscale.com/kb/1215/oauth-clients),
[trust-credentials reference](https://tailscale.com/docs/reference/trust-credentials)).

There are two ways to hand Once credentials. Option A (the default, and the only
one the TUI form offers - `internal/ui/tailscale_form.go:68-92`) is to create
the runtime credential yourself. Option B is to create a single broader
bootstrap credential and let Once mint the narrower clients from it
(`internal/command/tailscale.go:41-47`).

## Before you start

- You need an Owner, Admin, Network admin, or IT admin account to create trust
  credentials; only Owners and Admins can grant any scope and any tag
  ([kb/1215](https://tailscale.com/kb/1215/oauth-clients)).
- Pick the tag Once's node and Services will carry, e.g. `tag:once`. Once
  accepts a comma-separated list and prepends `tag:` to bare names
  (`internal/docker/tailscale.go:145-160`).
- The tag must already be defined in your tailnet policy's `tagOwners` before a
  credential can carry it: the console only offers tags "in the tailnet"
  ([kb/1215](https://tailscale.com/kb/1215/oauth-clients)), and API minting of a
  tagged client fails with a pointer to `tagOwners`
  (`internal/docker/tailscale_bootstrap.go:100`). On a fresh tailnet, add this
  to the policy file (https://login.tailscale.com/admin/acls) first:

  ```jsonc
  "tagOwners": {
      "tag:once": ["autogroup:admin"],
  },
  ```

  That one line is enough to create the credential. Once offers to write the
  remaining policy sections it needs during enable (see below), so you do not
  have to hand-edit the rest.

## Option A (default): create the runtime credential yourself

1. In the policy file, define the tag in `tagOwners` (see above).
2. Open the admin console's Trust credentials page:
   https://login.tailscale.com/admin/settings/trust-credentials
   ([kb/1215](https://tailscale.com/kb/1215/oauth-clients)).
3. Select the **Credential** button, then **OAuth**.
4. Select Write for exactly these three scopes, and assign your tag (e.g.
   `tag:once`) where the scope asks for tags. Each scope is a single
   Read-or-Write choice and Write includes Read, so Write alone is both
   necessary and sufficient - this is the minimum credential; the enable-time
   probe fails on anything narrower (`internal/docker/tailscale_validate.go`).
   Even Once's only devices call, the read-only
   `GET /services/{name}/devices`, is gated on the write-form `services` +
   `devices:core` scopes per the endpoint's own annotation in the
   [Tailscale API OpenAPI spec](https://api.tailscale.com/api/v2?outputOpenapiSchema=true),
   so Devices > Core cannot be attenuated to Read:
   - **Keys > Auth Keys: Write** (scope `auth_keys`) - lets Once mint the
     sidecar's auth key. Auth keys minted by the credential can only carry the
     credential's tags, or tags those tags own
     ([trust-credentials reference](https://tailscale.com/docs/reference/trust-credentials)).
   - **Devices > Core: Write** (scope `devices:core`) - lets Docktail read
     which devices advertise a Service. This scope requires selecting one or
     more tags
     ([trust-credentials reference](https://tailscale.com/docs/reference/trust-credentials)).
   - **General > Services: Write** (scope `services`) - lets Docktail create,
     update, and delete Tailscale Service definitions. The "General/Services"
     console wording is documented in the
     [Kubernetes operator install guide](https://tailscale.com/docs/kubernetes-operator/install-operator);
     the `services` scope identifier is documented on the Services endpoints of
     the [Tailscale API OpenAPI spec](https://api.tailscale.com/api/v2?outputOpenapiSchema=true).
5. Select **Generate credential** and copy the client ID and secret from the
   **Credential created** page. The secret cannot be copied again after you
   close that page, and it is case-sensitive
   ([kb/1215](https://tailscale.com/kb/1215/oauth-clients)).
6. Run:

   ```sh
   once tailscale enable --client-id <id> --client-secret <secret> --tag tag:once
   ```

   or enter the same three values in the TUI's Tailscale settings form.
7. Interactive enables then offer to write the four policy sections Once needs
   (`internal/command/tailscale.go:247-341`). If you accept, it prompts for a
   second, throwaway credential with **Policy File: Write** (scope
   `policy_file`, no tags): create it the same way, paste it, and revoke it on
   the Trust credentials page afterwards - it is used once and never stored.
   If you decline, Once prints a HuJSON snippet to merge into the policy file
   yourself. Note: the trust-credentials reference says `devices:posture_attributes`
   and `devices:core:read` "are required when using" the `policy_file` scope,
   so the console may make you select those alongside Policy File.

## Option B: one bootstrap credential, Once mints the rest

1. Create a trust credential with a single scope: **Keys > OAuth Keys: Write**
   (scope `oauth_keys`, no tags), which grants creating OAuth clients via
   `POST /api/v2/tailnet/{tailnet}/keys`
   ([OpenAPI spec](https://api.tailscale.com/api/v2?outputOpenapiSchema=true)).
   Treat it as admin-equivalent: minted clients' scopes are not attenuated to
   the creator's (`.scratch/docktail-migration/issues/17-bootstrap-credential-minting.md`).
2. Run:

   ```sh
   once tailscale enable --bootstrap-client-id <id> --bootstrap-client-secret <secret> --tag tag:once
   ```

3. Once first offers the policy write using a one-shot minted `policy_file`
   client that it deletes right after (a fresh tailnet must define the tag in
   `tagOwners` before a tagged client can be minted), then mints the tagged
   runtime client with scopes `auth_keys`, `devices:core`, `services`
   (`internal/docker/tailscale_bootstrap.go:12-67`,
   `internal/command/tailscale.go:232-245`). The bootstrap and policy
   credentials live only in memory; only the runtime credential is persisted.
4. Revoke the bootstrap credential on the Trust credentials page when enable
   finishes, unless you plan to reuse it.

## What Once does with the credential

- Exchanges it for an access token at `POST /api/v2/oauth/token`
  (`internal/docker/tailscale_validate.go:99-131`).
- Runs an enable-time probe before booting anything, byte-compatible with the
  runtime's real requests: mints and deletes a tagged, preauthorized,
  single-use auth key (`POST`/`DELETE /api/v2/tailnet/-/keys`), then creates,
  inspects, and deletes a throwaway Service `svc:once-probe`
  (`PUT`/`GET`/`DELETE /api/v2/tailnet/-/services/{name}` and
  `GET .../devices`) (`internal/docker/tailscale_validate.go:41-263`).
- In sidecar mode, mints the `once-tailscale` node's `TS_AUTHKEY` with exactly
  the probe's key shape (`internal/docker/tailscale_validate.go:82-93`).
- Passes the credential to Docktail, which syncs one Service definition per
  exposed app to the control plane (ADR-0002,
  `docs/adr/0002-docktail-tailscale-services.md`).
- The four policy sections it needs (written by the enable offer or pasted from
  the printed snippet, `internal/docker/tailscale_policy.go:109-134,239-252`):
  `tagOwners` (tag owned by `autogroup:admin`), `autoApprovers.services` (the
  tag approves its own Services, so new apps don't sit pending approval on the
  admin console's Services page), a grant from `autogroup:member` to the tag on
  port 443 (the default allow-all policy does not cover `svc:` destinations),
  and a `funnel` nodeAttr on the tag for `once tailscale funnel enable`
  ([kb/1552](https://tailscale.com/kb/1552/tailscale-services),
  [kb/1223](https://tailscale.com/kb/1223/funnel)).

## Plan and version requirements

- Tailscale Services is GA (announced 2026-02-19) on all plans, with a limit of
  10 Services per tailnet
  ([services-ga blog](https://tailscale.com/blog/services-ga)).
- Service hosts need Tailscale v1.86.0+ and a tag-based identity; Once's pinned
  sidecar image and minted tagged key satisfy both
  ([kb/1552](https://tailscale.com/kb/1552/tailscale-services), ADR-0002).

## Sources

- https://tailscale.com/kb/1215/oauth-clients (Trust credentials page, creation
  steps and UI labels, roles, tag rules, secret shown once; last validated
  2026-06-30)
- https://tailscale.com/docs/reference/trust-credentials (scope identifiers and
  per-scope endpoint tables, `devices:core` tag requirement, minted-key tag
  rule, `policy_file` companion-scope note)
- https://tailscale.com/docs/kubernetes-operator/install-operator ("General/
  Services", "Devices/Core", "Keys/Auth Keys" console scope wording)
- https://api.tailscale.com/api/v2?outputOpenapiSchema=true (`services`,
  `services:read`, `devices:core`, `auth_keys`, `oauth_keys`, `policy_file`
  scope-to-endpoint mapping, including `oauth_keys` on `POST /tailnet/{t}/keys`)
- https://tailscale.com/kb/1552/tailscale-services (tagged host requirement,
  v1.86.0+, autoApprovers.services, svc: grants, Services approval page)
- https://tailscale.com/kb/1223/funnel (funnel nodeAttr prerequisite)
- https://tailscale.com/blog/services-ga (GA date, all plans, 10-Service limit)
- /home/arau/code/once/docs/adr/0002-docktail-tailscale-services.md
- /home/arau/code/once/internal/command/tailscale.go:39-48,93-112,232-341
- /home/arau/code/once/internal/docker/tailscale_bootstrap.go:12-113
- /home/arau/code/once/internal/docker/tailscale_validate.go:14-263
- /home/arau/code/once/internal/docker/tailscale_policy.go:109-134,239-252
- /home/arau/code/once/internal/docker/tailscale.go:145-160
- /home/arau/code/once/internal/ui/tailscale_form.go:68-92
- /home/arau/code/once/.scratch/docktail-migration/issues/05-research-tailnet-prereqs.md
- /home/arau/code/once/.scratch/docktail-migration/issues/17-bootstrap-credential-minting.md
