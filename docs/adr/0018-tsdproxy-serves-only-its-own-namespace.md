# TSDProxy Serves Only Its Own Namespace

Narrows [ADR-0015](0015-tailnet-topology-seam-nodes-default.md) (the Nodes
topology's TSDProxy config and app label dialect).

TSDProxy watches every container on the Docker host labelled
`tsdproxy.enable=true`, not only its own namespace's, and keys its proxies by
`tsdproxy.name`. Two Once namespaces on one host (`--namespace` is a supported
flag) therefore served each other's apps, and a container of one namespace
starting or dying under a name the other also used closed the other's proxy.
This was seen live: integration-test apps joined the user's real tailnet through
the live `once-tsdproxy`, a dying test `once-admin` stopped the live
`once-admin` proxy, and the nodes integration tests failed intermittently with
`not yet advertised by the local tailscaled: once-admin` whenever the live
install's `once-admin` was running.

We decided each namespace's TSDProxy defines exactly one usable Tailscale
provider, named after the namespace, and every container it should serve names
that provider with `tsdproxy.proxyprovider=<namespace>`. TSDProxy v2.3.4
resolves a container's provider before it closes any existing proxy of the same
name (`proxymanager.newAndStartProxy`): a named provider the config lacks is
refused outright, and so are other namespaces' apps. A container naming no
provider falls back to the defaults; TSDProxy refuses to start when a default
names no provider at all, so both defaults (global and Docker provider) name a
second provider, `other_namespace`, that can never serve. It carries placeholder
OAuth credentials and no tags, so resolving its auth key fails: inside TSDProxy
for a container without a `tsdproxy.tags` label (every Once container), and
with a 401 from Tailscale for one that has it - either way before the old
proxy is closed. A provider with no credentials would instead resolve an empty
key and wait for an interactive login, closing the old proxy first. Namespace
names are lowercase letters, digits and hyphens, so the underscore keeps it
from ever equalling one, and TSDProxy's case-insensitive provider lookup cannot
confuse two namespaces.

TSDProxy keeps each node's tsnet state at `/data/<provider>/<node>`, and every
install before this decision used the provider `default`. Renaming the provider
without the state would re-register every node and Tailscale would dedupe each
name to `<name>-1`. Before every start of `once-tsdproxy`, against the created
container whose volumes Docker mounts for the archive API, Once creates
`/data/<namespace>` as a relative symlink to `default` when the legacy directory
exists and the provider's does not. A symlink rather than a move or copy: the
TSDProxy image has no shell, and one symlink is created whole or not at all, so
an interrupted boot leaves either the old layout or the new one. A namespace
named `default` needs no link.

Existing installs adopt this without a Once command. The daemon heals at
startup, and `once tailscale enable` does the same through boot: a
`once-tsdproxy` whose config, read back from the container, does not isolate the
namespace is rebuilt; then every running app and `once-admin` missing a
tailnet label the active topology would now write is rolled or recreated. The
check is driven by the labels themselves, so a heal interrupted partway
converges on its next run.

## Considered options

- **Rename the test suite's `once-admin`** so tests stop colliding with a live
  install. Rejected: it hides the symptom for one name in tests only, while two
  real namespaces on one host keep hijacking each other's nodes, including any
  app name they share.
- **Run integration tests against a separate dockerd.** Rejected for the same
  reason: it protects the developer's live install and leaves `--namespace`
  broken for everyone else.
- **Copy or move the legacy state** into `/data/<namespace>` instead of
  linking. Rejected: a move needs a helper image with a shell, and a copy
  through the archive API is not atomic, so an interrupted one can leave some
  nodes' state half-written; it also leaves the old directory behind.

## Consequences

- Migrated installs keep their state under `default` for good, reached through
  the link. Fresh installs keep it under `/data/<namespace>` directly.
- A TSDProxy from an older Once still running on the same host (a namespace not
  yet healed) refuses the new labels, since it knows only `default`; the reverse
  is what this decision prevents. A namespace literally named `default` is
  served by such an older TSDProxy until that one is healed.
- Each refused foreign container is logged by TSDProxy as a failed proxy start.
- The Services topology has the same shape of problem: Docktail watches every
  container's `docktail.*` labels and has no filter by label value or project.
  Left as a follow-up; see `.scratch/tsdproxy-namespace-isolation`.
