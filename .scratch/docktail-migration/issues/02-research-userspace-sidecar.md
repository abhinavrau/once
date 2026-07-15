# Research: Services + Funnel from a userspace-networking sidecar

Status: done
Labels: wayfinder:research
Blocked by: (none)

## Question

Can a tailscale/tailscale container running in userspace networking mode (no
/dev/net/tun, no NET_ADMIN) advertise Tailscale Services and serve Funnel, as
docktail drives it? Specifically:

- Does `tailscale serve` / Services advertisement work with
  TS_USERSPACE=true? Any documented restrictions?
- Does Funnel work in userspace mode?
- Can the userspace sidecar dial backend containers on a shared Docker bridge
  network (docktail's direct mode resolves container IPs)?
- If userspace mode is insufficient: exact minimum privileges for the sidecar
  (TUN, NET_ADMIN, host networking?) and whether docktail's documented sidecar
  setup (docker-compose.sidecar.yaml in /tmp/docktail) can be trimmed down.

Context: Once's ADR (docs/adr/0001-tailscale-integration.md) chose tsdproxy
partly to avoid TUN/NET_ADMIN. The charting decision is to support both host
tailscaled and a Once-managed sidecar; this determines what the sidecar costs
in privileges. Primary sources: tailscale.com/kb (userspace networking, serve,
funnel, services), tailscale GitHub issues, docktail code/docs in /tmp/docktail.

## Resolution

### Does `tailscale serve` / Services advertisement work with TS_USERSPACE=true?

Yes. TS_USERSPACE=true is the default of the tailscale/tailscale image ("Enabled
by default" per the Docker configuration parameters docs), and serve plus
Services advertisement run entirely inside tailscaled: inbound tailnet traffic
is terminated by the gVisor netstack, so no TUN device or NET_ADMIN is needed.
The documented userspace restrictions are about other things: outbound
connections from *other processes* into the tailnet need the SOCKS5/HTTP proxy,
and the egress modes TS_TAILNET_TARGET_IP / TS_TAILNET_TARGET_FQDN are "not
compatible with TS_USERSPACE mode". Neither applies to a docktail sidecar.
The one Services feature that does require a TUN is layer 3 (TUN-mode) Service
endpoints (`tailscale serve --service ... --tun`); the CLI restricts TUN mode to
services only, and docktail never uses it (it only issues
`serve --service=svc:X --http/--https/--tcp=PORT DEST`, see
/tmp/docktail/tailscale/service.go addService). The Tailscale Services docs even
recommend userspace-networking mode as the workaround for the multi-interface
Linux limitation, confirming it is a supported mode for Services.
Non-privilege requirements still apply: Tailscale v1.86.0+, a tag-based node
identity (docktail's sidecar sets TS_EXTRA_ARGS=--advertise-tags=tag:server),
and per-Service approval in the admin console or an ACL autoApprovers rule.
Related container caveat: Services defined statically via TS_SERVE_CONFIG were
not auto-advertised (tailscale/tailscale#18255, closed); docktail is unaffected
because it configures and advertises via the CLI at runtime.

### Does Funnel work in userspace mode?

Yes. Funnel ingress arrives via Tailscale's relay over the node's existing
outbound tailnet connection and is handled by netstack; Tailscale's own Docker
guide demonstrates serve and funnel from an unprivileged sidecar container in
default userspace mode. Gating is policy, not privileges: the node needs the
`funnel` nodeAttr in the tailnet policy (docktail detects this failure mode in
funnel.go isFunnelACLError). GitHub issue tailscale/tailscale#12788 (funnel
failing with --tun=userspace-networking) is specific to running tailscaled as a
non-root user on Termux where `tailscale cert` cannot adjust DNS; a root
container process is not affected. Note funnel is per-node (machine hostname)
and cannot be combined with Services: the CLI rejects `--service` with funnel
("--service flag is not supported with funnel"), which matches docktail
treating funnel as independent of Services.

### Can the userspace sidecar dial backends on a shared Docker bridge network?

Yes. The serve/funnel backend hop is an ordinary outbound OS-socket dial made
by the tailscaled process, unaffected by userspace mode. Non-localhost backends
are accepted by current tailscale: ipn.ExpandProxyTargetValue validates any
valid hostname or IP ("allow no port for remote destinations"); the old
localhost-only restriction (issue #8751, and a stale "only http://127.0.0.1 is
supported for proxies" line still in the serve CLI docs) was lifted with the
Services work. docktail relies on this: buildDestination and
desiredFunnelDestination pass `proto://<container-IP>:<port>` directly. The
only requirement is IP reachability: the sidecar must share a Docker network
with the backend containers (or use host networking). Two docktail modes would
NOT work from a non-host-network sidecar: `docktail.service.direct=false`
(published host ports, resolves to host localhost) and backends running with
`network_mode: host` (reached via localhost per docs/07-how-it-works.md). Once
controls its own bridge network, so direct mode on a shared network is the
natural fit; `docktail.service.network` selects which network's IP is used.

### If userspace were insufficient: minimum privileges, and can the docktail sidecar be trimmed?

Userspace IS sufficient, so the question inverts: docktail's documented sidecar
(docker-compose.sidecar.yaml: TS_USERSPACE=false, /dev/net/tun, cap_add
NET_ADMIN + SYS_MODULE, network_mode: host) is over-privileged for what
docktail actually does. Nothing in docktail requires kernel mode; it only
shells out to the tailscale CLI over the shared TS_SOCKET volume. The trimmed
sidecar is: tailscale/tailscale with TS_USERSPACE left at its default (true),
TS_AUTHKEY (tagged), TS_STATE_DIR on a volume, TS_SOCKET on a volume shared
with the docktail container, attached to the Once app bridge network - no
cap_add, no devices, no host networking. SYS_MODULE was only ever needed to
modprobe tun from inside the container; /dev/net/tun and NET_ADMIN only for
TS_USERSPACE=false. Kernel mode remains an opt-in for throughput (netstack is
slower than a kernel TUN) or for TUN-mode L3 Service endpoints, neither of
which Once needs for HTTP app publishing.

### Bottom line

A Once-managed sidecar can advertise Tailscale Services and serve Funnel via
docktail with zero elevated privileges: an unprivileged tailscale/tailscale
container in default userspace-networking mode (no /dev/net/tun, no NET_ADMIN,
no SYS_MODULE, no host networking), joined to the shared app bridge network,
with a state volume, a tailscaled-socket volume shared with docktail, and a
tag-advertising auth key. The real prerequisites are tailnet policy, not
privileges: v1.86+, tag-based identity, Service approval/autoApprovers, and the
funnel nodeAttr. This keeps the ADR's no-TUN/no-NET_ADMIN goal intact even in
the sidecar deployment shape.

### Sources

- Tailscale KB: userspace networking mode - https://tailscale.com/kb/1112/userspace-networking
- Tailscale docs: Docker configuration parameters (TS_USERSPACE default, kernel-mode requirements, TS_TAILNET_TARGET incompatibility) - https://tailscale.com/docs/features/containers/docker/docker-params
- Tailscale docs: Tailscale Services (v1.86+, tag-based identity, approval, userspace workaround, --tun L3 endpoints) - https://tailscale.com/docs/features/tailscale-services
- Tailscale docs: serve CLI reference (stale localhost-only note; --service flag) - https://tailscale.com/docs/reference/tailscale-cli/serve
- Tailscale blog: Docker deep dive (serve/funnel from userspace sidecar; TS_SERVE_CONFIG; AllowFunnel needs funnel nodeAttr) - https://tailscale.com/blog/docker-tailscale-guide
- tailscale/tailscale ipn/serve.go ExpandProxyTargetValue (remote backends allowed) - https://github.com/tailscale/tailscale/blob/main/ipn/serve.go
- tailscale/tailscale cmd/tailscale/cli/serve_v2.go (--service rejected with funnel; TUN mode services-only) - https://github.com/tailscale/tailscale/blob/main/cmd/tailscale/cli/serve_v2.go
- GitHub issue #8751: historical localhost-only serve/funnel proxy restriction - https://github.com/tailscale/tailscale/issues/8751
- GitHub issue #12788: funnel + userspace as non-root (cert/DNS, Termux-specific) - https://github.com/tailscale/tailscale/issues/12788
- GitHub issue #18255: TS_SERVE_CONFIG Services not auto-advertised in container - https://github.com/tailscale/tailscale/issues/18255
- docktail checkout: /tmp/docktail/docker-compose.sidecar.yaml, docs/02-installation.md, docs/04-labels.md, docs/07-how-it-works.md, tailscale/service.go, tailscale/funnel.go, tailscale/utils.go (buildDestination)
