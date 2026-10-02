# Tailnet Topology is a seam under the Tailscale facade; per-node is the default

Supersedes the topology choice of [ADR-0002](0002-docktail-tailscale-services.md)
(Docktail and Tailscale Services as the one way onto the tailnet). Narrows
[ADR-0003](0003-single-exposure-model.md) (the Host under tailnet is
`{app}.{suffix}`, not "the Tailscale Service name"), [ADR-0007](0007-sidecar-default-tailscale-mode.md)
and [ADR-0010](0010-tailscale-mode-daemon-single-doors.md) (mode resolution and
daemon binding are now facts of one topology, not of the integration).

ADR-0002 replaced per-app tailnet devices with one shared node advertising
Tailscale Services. Two things changed since. Services are capped on the free
plan while tagged nodes are not, so a self-hoster with a dozen apps runs out of
Services first; and headscale, which Once must be able to target in future, has
no Services, Serve, or Funnel - only plain nodes join it. Both point back at one
node per application. We did not want to swap providers a second time by clean
break and lose the Services shape, which stays the right answer for a small
install on SaaS Tailscale. ADR-0010 and ADR-0011 deferred a provider seam
"until a second provider actually exists"; it now does.

We decided the axis is **Tailnet Topology**, named by shape and not by
software, with exactly two values: `services` (one shared node advertises a
Tailscale Service per app - Docktail, ADR-0002 unchanged behind the seam) and
`nodes` (one tailnet node per app - TSDProxy in per-node tsnet mode, pinned to
its v2 stable line; its v3 Services mode is never a third adapter). The
topology is an install-wide setting on the provider container's `once` label,
always written explicitly, chosen by `--topology` at enable and by a selector
in the TUI. `nodes` is the default for a fresh enable. Changing topology on a
live install rebuilds the topology and rolls every tailnet app, exactly as
changed credentials do; leaving `nodes` best-effort deletes the per-app devices
through the Runtime Credential, and leaving `services` best-effort deletes the
Tailscale Services it advertised, since each one holds a Magic DNS name.

The seam is private and sits under the `Tailscale` facade, which stays
concrete and deep: settings on the label, enabled-by-existence, rebuild-on-
change, the retrofit Roll, bounded Funnel duration, suffix and URL
persistence, and the Control Seam accessor do not move. What varies per
topology lives in one adapter each: boot/remove/destroy of the containers, the
app label dialect, the enable-time credential probe (byte-compatible with what
that provider sends at runtime), the tailnet reader behind Session (host CLI
or sidecar exec for `services`, TSDProxy's REST API for `nodes`), verify,
Funnel port allocation and public URL shape, host-name validation, and the
static facts the frontends render (default settings, OAuth scopes, policy
snippet, mode choices, setup prose). Mode (host/sidecar) exists only under
`services`: `nodes` has no tailscaled, so ADR-0007's default and ADR-0010's
mode and daemon doors belong to the Services adapter; ADR-0010's session and
control-seam doors stay in the facade.

Under `nodes`, tailnet traffic goes through the Proxy, not straight to the
app container: each app node forwards HTTP with the Host preserved to
`once-proxy`, which routes by host as it does for local and public apps.
This is a fact of the topology, not a knob. It gives every app node a stable
target so a Roll never touches TSDProxy, it makes tailnet apps visible to the
Traffic and Visitors cards for free, and it is the only shape that can carry
headscale later (no `tailscale cert` there, so TLS must terminate in the
Proxy). We build only the SaaS path now, where TSDProxy holds the certificate
and forwards plain HTTP; TCP passthrough with a Proxy-held certificate is the
adapter's decision to make later, not the facade's. Under `services` traffic
stays direct, because Docktail can only target the labelled container. The
Host under `nodes` must equal `{app}.{suffix}`: a tailnet device that already
holds the name is refused at deploy time rather than accepting a `-1` alias.

Consequences and accepted costs:

- No backward compatibility. A Docktail install from before this decision has
  no `topology` in its label; the read paths report that the setup is from an
  older Once and ask for `once tailscale enable`, which rebuilds it as `nodes`
  unless `--topology services` is given. The legacy TSDProxy cleanup path from
  ADR-0002 is deleted first, so `once-tsdproxy` is free for the new adapter.
- Via-Proxy tailnet apps answer on the public IP to a spoofed Host header,
  exactly as local apps already do. Accepted for now; the shared fix (a Proxy
  listener not published on host ports for non-public exposures) is a separate
  ticket.
- The Services shape costs a Service per app; the nodes shape costs a tagged
  device per app. Once takes no position on which limit the user hits first;
  it names the shapes so the user can.
- Per-node on headscale is integration-testable through the existing Control
  Seam (control URL plus pre-auth key), so the default topology gains
  end-to-end coverage that Services never had; the SaaS key-mint probe stays
  under httptest.
