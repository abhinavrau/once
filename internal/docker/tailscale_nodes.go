package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	"gopkg.in/yaml.v3"
)

// tsdproxyImage is pinned exactly, the same doctrine as docktailImage: bumped
// via Once releases. v2.3.4 is the last of TSDProxy's v2 per-node-tsnet
// stable line (ADR-0015) - v3's Services/VIP mode is never a third adapter.
const tsdproxyImage = "almeidapaulopt/tsdproxy:2.3.4"

// tsdproxyAdminContainerPort is TSDProxy's own default admin API port
// (serverconfig.md: http.port defaults to 8080), published on loopback only,
// on a Docker-picked host port so every namespace on the host gets its own.
const tsdproxyAdminContainerPort = 8080

// tsdproxyConfigPath is where TSDProxy reads its config file from inside the
// container (serverconfig.md's default, which Once does not override).
const tsdproxyConfigPath = "/config"

// nodesDataVolumeTarget is where TSDProxy keeps each app's tsnet state,
// TailscaleProxyProviderConfig.DataDir's default.
const nodesDataVolumeTarget = "/data"

// tsdproxyUnclaimedProvider is the provider TSDProxy falls back to for a
// container naming none - another namespace's from before ADR-0018, or one
// that is not Once's at all. TSDProxy v2.3.4 refuses to start with a default
// that names no provider, so it exists, but can never serve: its OAuth client
// has no tags, so minting a key fails before any request leaves TSDProxy (an
// app's tsdproxy.tags label would at most get a 401), and the proxy manager
// resolves that key before it closes any existing proxy of the same name.
// The underscore keeps it from ever equalling a namespace name.
const tsdproxyUnclaimedProvider = "other_namespace"

// unclaimedProviderConfig carries placeholder OAuth credentials because only
// the OAuth path fails to resolve a key: a provider with no credentials at all
// resolves an empty key and waits for an interactive login instead.
var unclaimedProviderConfig = tsdproxyTailscaleProviderConfig{
	ClientID:     "unclaimed",
	ClientSecret: "unclaimed",
}

// nodesFunnelPort is the public port every nodes-topology Funnel serves on.
// Unlike Services, which shares one node's ports across apps and so must
// allocate one per app, every nodes app is its own node: Funnel is a
// per-app on/off with nothing to allocate, and 443 is the only port TSDProxy
// ever offers Funnel on for a node's own hostname.
const nodesFunnelPort = 443

// remintCredentialHint is the fix for a Nodes credential missing a scope:
// Tailscale fixes an OAuth client's scopes when it is created, so the client
// cannot be edited into shape, only replaced.
const remintCredentialHint = "scopes are fixed when an OAuth client is created, so create a new OAuth client with write access to Auth Keys and Devices Core and read access to Services (see 'once tailscale enable --help'), then re-run 'once tailscale enable' with it"

// nodesSetupIntro is the Nodes topology's setup prose, shown before enable
// via TopologyFacts.SetupIntro (ticket 05).
const nodesSetupIntro = `Setup, once per tailnet:
 1. Run 'once tailscale policy --topology nodes' and merge the printed
    snippet into your tailnet policy file (https://login.tailscale.com/admin/acls/file).
 2. In the admin console (Settings -> Trust credentials), create an OAuth
    client with write access to Auth Keys and Devices Core, and read
    access to Services, tagged with your tag (default tag:once). Services
    lets Once spot a Tailscale Service holding an app's name, which would
    otherwise give its node a "-1" suffix.`

var _ tailnetTopology = (*nodesTopology)(nil)

// nodesTopology is the Nodes topology's adapter (ADR-0015): one tailnet node
// per Application, implemented by TSDProxy in per-node tsnet mode.
// Application traffic goes through the Proxy - every app's tsdproxy.* labels
// target once-proxy with the inbound Host preserved, never the app
// container - so a Roll never touches once-tsdproxy, and tailnet apps are
// visible to the Traffic and Visitors cards for free.
type nodesTopology struct {
	namespace *Namespace
}

func newNodesTopology(ns *Namespace) *nodesTopology {
	return &nodesTopology{namespace: ns}
}

// boot idempotently ensures once-tsdproxy is running with settings written
// into its config file and its own label. One whose config would let it serve
// other namespaces' containers - booted before ADR-0018, or created by a boot
// interrupted before its config was written - is rebuilt.
func (n *nodesTopology) boot(ctx context.Context, settings TailscaleSettings) error {
	info, err := n.namespace.client.ContainerInspect(ctx, n.containerName())
	switch {
	case err == nil:
		isolates, err := n.isolates(ctx, info.ID)
		if err != nil {
			return err
		}
		if !isolates {
			return n.relabel(ctx, settings)
		}
		if !info.State.Running {
			return n.start(ctx, info.ID)
		}
		return nil
	case !errdefs.IsNotFound(err):
		return fmt.Errorf("inspecting tsdproxy container: %w", err)
	}

	if err := pullImageRef(ctx, n.namespace.client, tsdproxyImage); err != nil {
		return err
	}
	return n.create(ctx, settings)
}

// heal is boot for an install that already runs once-tsdproxy, so the daemon
// brings one booted by an older Once up to date at startup without creating
// one an install does not have.
func (n *nodesTopology) heal(ctx context.Context, settings TailscaleSettings) error {
	if _, err := n.namespace.client.ContainerInspect(ctx, n.containerName()); err != nil {
		if errdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("inspecting tsdproxy container: %w", err)
	}
	return n.boot(ctx, settings)
}

// remove tears down once-tsdproxy, keeping its data volume (every app's tsnet
// state), and best-effort deletes the per-app tailnet devices through the
// Runtime Credential (ADR-0015: leaving nodes must not leave orphaned tagged
// devices sitting in the tailnet under a topology that no longer manages
// them).
func (n *nodesTopology) remove(ctx context.Context, settings TailscaleSettings) error {
	if err := forceRemoveContainer(ctx, n.namespace.client, n.containerName()); err != nil {
		return err
	}
	n.bestEffortDeleteTaggedDevices(ctx, settings, "")
	return nil
}

// leave is remove: nodes holds Magic DNS names only through its devices,
// which every remove already sweeps.
func (n *nodesTopology) leave(ctx context.Context, settings TailscaleSettings) error {
	return n.remove(ctx, settings)
}

// destroy removes everything leave does, plus the data volume - full
// cleanup, used by teardown.
func (n *nodesTopology) destroy(ctx context.Context, settings TailscaleSettings) error {
	if err := n.leave(ctx, settings); err != nil {
		return err
	}
	if err := n.namespace.client.VolumeRemove(ctx, n.dataVolumeName(), true); err != nil {
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("removing volume %s: %w", n.dataVolumeName(), err)
		}
	}
	return nil
}

// removeApp best-effort deletes the removed app's own tailnet device, narrowed
// to its Host so no other tagged device is touched - the single-app
// counterpart of remove's sweep, without which every removal leaves an orphan
// device holding the Host.
func (n *nodesTopology) removeApp(ctx context.Context, settings TailscaleSettings, host string) {
	n.bestEffortDeleteTaggedDevices(ctx, settings, host)
}

// relabel rewrites once-tsdproxy's settings label and config file by
// recreating it (Docker labels are immutable on a live container, and
// TSDProxy's own credentials only ever come from its config file). The data
// volume is untouched - used to bake in a newly-learned domain suffix.
func (n *nodesTopology) relabel(ctx context.Context, settings TailscaleSettings) error {
	if err := forceRemoveContainer(ctx, n.namespace.client, n.containerName()); err != nil {
		return err
	}
	return n.create(ctx, settings)
}

// appLabels returns the labels that expose an app as a TSDProxy node under
// its own Host: the node's tailnet hostname (tsdproxy.name), and its one port
// pointed at once-proxy's port 80 with auto-detection off, so the target is
// always the Proxy's routing layer - never the app container - regardless of
// which network the app itself is on. network is unused: TSDProxy resolves
// the target from the Docker provider's own targetHostname config
// (tsdproxy.yaml), the same for every app, not per-container.
//
// The node's own edge listener is HTTPS (443) on SaaS, where TSDProxy holds
// a real tailnet certificate (ADR-0015) - but headscale's Control Seam has
// no `tailscale cert` capability at all: tsnet refuses to listen TLS at all
// without it ("you must enable HTTPS in the admin panel"), so the Control
// Seam listens plain HTTP on 80 instead. Funnel needs Funnel itself, which
// headscale does not implement either, so this only ever matters for the
// happy SaaS path in practice - but the port must still name a valid
// protocol under the seam, or every node fails to start.
func (n *nodesTopology) appLabels(host, network string, funnelPort int) map[string]string {
	port := "443/https:80/http, no_autodetect"
	if controlSeamActive() {
		port = "80/http:80/http, no_autodetect"
	}
	if funnelPort != 0 {
		port += ", tailscale_funnel"
	}
	return map[string]string{
		"tsdproxy.enable":        "true",
		"tsdproxy.name":          host,
		"tsdproxy.port.1":        port,
		"tsdproxy.proxyprovider": n.providerName(),
	}
}

// probe is the enable-time credential check: it proves the OAuth client can
// mint a per-node auth key with the exact request shape TSDProxy sends at
// runtime, and can list the tailnet's devices and Services - everything
// nodes needs the Runtime Credential for. Skipped on the hidden headscale control seam, which
// uses a pre-minted auth key, not OAuth. Nodes has no topology-owned
// defaulting (no Mode), so it always returns settings unchanged and no
// notice.
func (n *nodesTopology) probe(ctx context.Context, settings TailscaleSettings) (TailscaleSettings, string, error) {
	if controlSeamActive() {
		return settings, "", nil
	}
	if err := validateNodesCredentials(ctx, newTailscaleAPI(settings), settings); err != nil {
		return TailscaleSettings{}, "", err
	}
	return settings, "", nil
}

// reader returns the TailnetReader bound to TSDProxy's admin API on its
// published loopback port, looked up on every read since Docker may pick a
// different one each time the container starts.
func (n *nodesTopology) reader(settings TailscaleSettings) TailnetReader {
	return newTSDProxyReader(n.adminURL)
}

// verify confirms every expected app has joined and is being served by
// TSDProxy, opportunistically learning and persisting the Magic DNS suffix
// along the way (nodes has no shared node to read it from at boot, unlike
// Services - the first node to join is whichever app or once-admin gets
// there first).
func (n *nodesTopology) verify(ctx context.Context, settings TailscaleSettings, expected []string, progress DeployProgressCallback) error {
	reader := n.reader(settings)

	if settings.DomainSuffix == "" {
		if suffix, _ := waitForMagicDNSSuffix(ctx, reader, suffixWaitTimeout, suffixWaitPoll); suffix != "" {
			settings.DomainSuffix = suffix
			_ = n.namespace.tailscale.persistSettings(ctx, settings)
		}
	}

	announce := func(line string) {
		if progress != nil {
			progress(DeployProgress{Stage: DeployStageWaiting, Status: line})
		}
	}
	return waitForAdvertised(ctx, reader, expected, n.logsHint(), serviceVerifyTimeout, serviceVerifyPoll, announce)
}

// status reports each named app's current tsdproxy state: joined-and-running,
// or not (naming the logs to check). Nodes has no separate cloud-approval
// step the way Services does - a minted, preauthorized key joins the tailnet
// immediately.
func (n *nodesTopology) status(ctx context.Context, settings TailscaleSettings, names []string) ([]ServiceStatus, error) {
	serve, err := n.reader(settings).ServeStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading tsdproxy status: %w", err)
	}

	statuses := make([]ServiceStatus, 0, len(names))
	for _, name := range names {
		state := fmt.Sprintf("not advertised (check %s)", n.logsHint())
		if slices.Contains(serve.Services, name) {
			state = "advertised"
		}
		statuses = append(statuses, ServiceStatus{
			Name:  name,
			URL:   settings.TailnetURLFor(name, serve.FQDNs),
			State: state,
		})
	}
	return statuses, nil
}

// funnelPort always returns the fixed Funnel port: nodes needs no
// allocation, since every app is its own node with its own 443.
func (n *nodesTopology) funnelPort(self *Application, apps []*Application) (int, error) {
	return nodesFunnelPort, nil
}

// validateHost rejects a name that cannot be a Magic DNS label, and - live,
// skipped on the Control Seam - refuses a deploy whose Host is already held
// by an existing tailnet device, so TSDProxy never falls back to a "-1"
// alias (ADR-0015). An app redeploying under a Host its own node already
// holds is not a collision: it is exempted by checking TSDProxy's own live
// state first. A device carrying Once's own tag with no node backing it is
// a leftover of a failed or removed deploy and is swept rather than refused.
func (n *nodesTopology) validateHost(ctx context.Context, settings TailscaleSettings, name string) error {
	if err := ValidateTailscaleServiceName(name); err != nil {
		return err
	}
	if controlSeamActive() {
		return nil
	}
	servicesScopeMissing, err := validateNodesHostAgainstTailnet(ctx, n.reader(settings), newTailscaleAPI(settings), name, settings.Tag)
	if servicesScopeMissing {
		slog.Warn("The Tailscale credential lacks the services:read scope, so only devices were checked for a holder of this Host",
			"host", name, "fix", remintCredentialHint)
	}
	return err
}

// logsHint names the container whose logs explain a tailnet read that came
// back short.
func (n *nodesTopology) logsHint() string {
	return "the " + n.containerName() + " container logs"
}

// facts returns the Nodes topology's static, context-free data.
func (n *nodesTopology) facts() TopologyFacts {
	return TopologyFacts{
		DefaultSettings: TailscaleSettings{Tag: "tag:once", Topology: TailnetTopologyNodes},
		OAuthScopes:     []string{"auth_keys:write", "devices:core:write", "services:read", "policy_file:read", "devices:posture_attributes:read"},
		PolicySnippet:   NodesPolicySnippet,
		ModeChoices:     nil,
		SetupIntro:      nodesSetupIntro,
	}
}

// Private

func (n *nodesTopology) containerName() string {
	return n.namespace.name + "-tsdproxy"
}

// providerName is the one Tailscale provider this namespace's TSDProxy
// defines, and the one every app it serves names (ADR-0018). It is also the
// directory under the data volume holding each node's tsnet state.
func (n *nodesTopology) providerName() string {
	return n.namespace.name
}

func (n *nodesTopology) dataVolumeName() string {
	return n.namespace.name + "-tsdproxy-data"
}

// create boots once-tsdproxy: Docker socket read-only (container discovery
// and target resolution only - TSDProxy never needs to start or stop a
// container), the data volume for every app's tsnet state, and the admin API
// published to loopback only. Config (credentials, the docker target
// provider's fixed once-proxy target) can only reach TSDProxy through its
// config file - v2.3.4 has no env var for OAuth credentials or
// targetHostname - so it is written into the container before it starts.
func (n *nodesTopology) create(ctx context.Context, settings TailscaleSettings) error {
	config, err := n.buildConfig(settings)
	if err != nil {
		return fmt.Errorf("building tsdproxy config: %w", err)
	}

	// A data volume that does not exist yet means once-admin's tsnet state
	// is about to start from scratch, so a tagged once-admin device is an
	// orphan of a previous enable - and once-admin never passes
	// validateHost's per-app sweep. Delete it before TSDProxy registers a
	// fresh node alongside the stale row. Only once-admin: app leftovers
	// are swept at their own deploys, and a tailnet-wide sweep here would
	// also take the services sidecar identity a topology switch keeps.
	if _, err := n.namespace.client.VolumeInspect(ctx, n.dataVolumeName()); errdefs.IsNotFound(err) {
		n.bestEffortDeleteTaggedDevices(ctx, settings, "once-admin")
	}

	resp, err := n.namespace.client.ContainerCreate(ctx,
		&container.Config{
			Image: tsdproxyImage,
			Labels: map[string]string{
				labelKey: settings.Marshal(),
			},
			ExposedPorts: nat.PortSet{tsdproxyAdminPort(): struct{}{}},
		},
		&container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyAlways},
			LogConfig:     ContainerLogConfig(),
			Binds:         []string{"/var/run/docker.sock:/var/run/docker.sock:ro"},
			Mounts: []mount.Mount{
				{Type: mount.TypeVolume, Source: n.dataVolumeName(), Target: nodesDataVolumeTarget},
			},
			PortBindings: tsdproxyAdminPortBindings(),
		},
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				n.namespace.name: {},
			},
		},
		nil,
		n.containerName(),
	)
	if err != nil {
		return fmt.Errorf("creating tsdproxy container: %w", err)
	}

	if err := n.writeConfig(ctx, resp.ID, config); err != nil {
		n.namespace.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return err
	}

	if err := n.start(ctx, resp.ID); err != nil {
		n.namespace.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return err
	}
	return nil
}

// start starts once-tsdproxy, first carrying any pre-ADR-0018 node state over
// to this namespace's provider. Every start does, not just create's, so a
// container left created-but-unstarted by an interrupted boot still gets it.
func (n *nodesTopology) start(ctx context.Context, containerID string) error {
	if err := linkLegacyNodeState(ctx, n.namespace.client, containerID, n.providerName()); err != nil {
		return err
	}
	if err := n.namespace.client.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return fmt.Errorf("starting tsdproxy container: %w", err)
	}
	return nil
}

// isolates reads back the config once-tsdproxy runs with and reports whether
// it keeps TSDProxy to this namespace's apps. A missing or unparseable config
// does not, so the caller rebuilds rather than trusting it; a failed read is
// an error, so a passing Docker hiccup never rebuilds a healthy TSDProxy.
func (n *nodesTopology) isolates(ctx context.Context, containerID string) (bool, error) {
	raw, err := readContainerFile(ctx, n.namespace.client, containerID, tsdproxyConfigPath+"/tsdproxy.yaml")
	if errdefs.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading tsdproxy config: %w", err)
	}
	return n.configIsolates(raw), nil
}

// adminURL is the base URL of TSDProxy's admin API on whatever loopback port
// Docker published it on.
func (n *nodesTopology) adminURL(ctx context.Context) (string, error) {
	info, err := n.namespace.client.ContainerInspect(ctx, n.containerName())
	if err != nil {
		return "", fmt.Errorf("inspecting tsdproxy container: %w", err)
	}
	return tsdproxyAdminURL(info)
}

// writeConfig copies tsdproxy.yaml into the container's config directory
// before it starts, the same CopyToContainer doctrine proxy.go's copyFiles
// uses for kamal-proxy's TLS files. TSDProxy's image is FROM scratch (no
// shell, no directory tree at all beyond the binary), so tsdproxyConfigPath
// does not exist yet for CopyToContainer to write into directly - the tar
// entry carries the full "config/tsdproxy.yaml" path instead, extracted
// under "/", which always exists, so the config directory is created as
// part of the extraction.
func (n *nodesTopology) writeConfig(ctx context.Context, containerID string, config []byte) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	header := &tar.Header{Name: strings.TrimPrefix(tsdproxyConfigPath, "/") + "/tsdproxy.yaml", Mode: 0o600, Size: int64(len(config))}
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("writing tar header: %w", err)
	}
	if _, err := tw.Write(config); err != nil {
		return fmt.Errorf("writing tar data: %w", err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("closing tar writer: %w", err)
	}
	if err := n.namespace.client.CopyToContainer(ctx, containerID, "/", &buf, container.CopyToContainerOptions{}); err != nil {
		return fmt.Errorf("writing tsdproxy config: %w", err)
	}
	return nil
}

// buildConfig renders tsdproxy.yaml (serverconfig.md's schema): one Docker
// target provider whose targetHostname is fixed at once-proxy - every app's
// tsdproxy.port.1 label targets container port 80 there, so this one setting
// is what makes every app's traffic land on the Proxy - and one Tailscale
// provider carrying the Runtime Credential (or the Control Seam's pre-minted
// key), tagged. adminAllowLocalhost is set explicitly rather than relied on
// as TSDProxy's own auto-Docker-detected default, so a future TSDProxy
// version changing that default cannot silently lock Once out of its own
// admin API.
//
// TSDProxy watches every container on the Docker host, so the config also
// keeps it off other namespaces' containers (ADR-0018): its one usable
// provider is this namespace's, which only this namespace's apps name, and
// both defaults point at tsdproxyUnclaimedProvider.
func (n *nodesTopology) buildConfig(settings TailscaleSettings) ([]byte, error) {
	cfg := tsdproxyConfigFile{
		DefaultProxyProvider: tsdproxyUnclaimedProvider,
		Docker: map[string]tsdproxyDockerProviderConfig{
			"local": {
				Host:                 "unix:///var/run/docker.sock",
				TargetHostname:       n.namespace.name + "-proxy",
				DefaultProxyProvider: tsdproxyUnclaimedProvider,
			},
		},
		Tailscale: tsdproxyTailscaleSection{
			Providers: map[string]tsdproxyTailscaleProviderConfig{
				n.providerName():          nodesProviderConfig(settings),
				tsdproxyUnclaimedProvider: unclaimedProviderConfig,
			},
			DataDir: nodesDataVolumeTarget + "/",
		},
		HTTP:                tsdproxyHTTPConfig{Hostname: "0.0.0.0", Port: tsdproxyAdminContainerPort},
		AdminAllowLocalhost: true,
	}
	return yaml.Marshal(cfg)
}

// configIsolates reports whether a tsdproxy.yaml keeps TSDProxy to this
// namespace's apps. A TSDProxy Once booted before ADR-0018, whose single
// "default" provider serves every unlabelled container on the host, does not.
func (n *nodesTopology) configIsolates(raw []byte) bool {
	var cfg tsdproxyConfigFile
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return false
	}
	_, ownProvider := cfg.Tailscale.Providers[n.providerName()]
	return ownProvider &&
		cfg.DefaultProxyProvider == tsdproxyUnclaimedProvider &&
		cfg.Docker["local"].DefaultProxyProvider == tsdproxyUnclaimedProvider
}

// bestEffortDeleteTaggedDevices deletes every tailnet device carrying
// settings.Tag through the Runtime Credential's devices:core scope
// (ADR-0015: leaving nodes best-effort deletes the per-app devices) - every
// device that tag can name is one nodesTopology itself created, since the
// tag is documented as Once's own. Best effort: an unreachable API, missing
// credentials, or an unset tag are not errors - this must never fail a
// topology switch or a teardown that is otherwise done. Skipped entirely on
// the Control Seam, which has no SaaS devices API.
// hostname narrows the sweep to devices bearing that one name; "" sweeps
// every tagged device.
func (n *nodesTopology) bestEffortDeleteTaggedDevices(ctx context.Context, settings TailscaleSettings, hostname string) {
	if api, ok := sweepAPI(settings); ok {
		deleteTaggedDevices(ctx, api, settings.Tag, hostname)
	}
}

// Helpers

func tsdproxyAdminPort() nat.Port {
	return nat.Port(fmt.Sprintf("%d/tcp", tsdproxyAdminContainerPort))
}

// tsdproxyAdminPortBindings publishes the admin API on loopback with an empty
// HostPort, which has Docker pick a free port.
func tsdproxyAdminPortBindings() nat.PortMap {
	return nat.PortMap{
		tsdproxyAdminPort(): []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: ""}},
	}
}

// tsdproxyAdminURL reads the admin API's published loopback port from a
// once-tsdproxy inspect. It reads whatever port is actually bound, so a
// container created while the port was fixed at 8180 keeps working as-is.
func tsdproxyAdminURL(info container.InspectResponse) (string, error) {
	if info.NetworkSettings != nil {
		for _, binding := range info.NetworkSettings.Ports[tsdproxyAdminPort()] {
			if binding.HostPort != "" {
				return "http://" + net.JoinHostPort("127.0.0.1", binding.HostPort), nil
			}
		}
	}
	return "", fmt.Errorf("tsdproxy's admin API port %s is not published (is the container running?)", tsdproxyAdminPort())
}

// validateNodesHostAgainstTailnet is validateHost's live check, factored out
// so it is testable against a fake reader and a fake tailscaleAPI without a
// real TSDProxy or Tailscale API to talk to. servicesScopeMissing reports a
// credential without services:read, whose check degraded to devices only.
func validateNodesHostAgainstTailnet(ctx context.Context, reader TailnetReader, api tailscaleAPI, name, tag string) (servicesScopeMissing bool, err error) {
	status, readErr := reader.ServeStatus(ctx)
	if readErr == nil && slices.Contains(status.Services, name) {
		return false, nil
	}
	servicesScopeMissing, err = refuseIfServiceHoldsName(ctx, api, name, tag)
	if err != nil {
		return false, err
	}
	if readErr != nil {
		// With TSDProxy unreadable (restarting, relabeling) a colliding
		// device may be this app's own live node - the sweep must not
		// revoke it, so a silent reader downgrades to the plain refusal.
		tag = ""
	}
	return servicesScopeMissing, refuseIfDeviceExists(ctx, api, name, tag)
}

// nodesProviderConfig builds the one Tailscale provider TSDProxy's config
// carries: the Control Seam's pre-minted key and control URL when it is
// active, else the Runtime Credential's OAuth client - never both, matching
// how Services' create() picks between them.
func nodesProviderConfig(settings TailscaleSettings) tsdproxyTailscaleProviderConfig {
	if authKey, controlURL, ok := controlSeam(); ok {
		return tsdproxyTailscaleProviderConfig{AuthKey: authKey, ControlURL: controlURL, Tags: settings.Tag}
	}
	return tsdproxyTailscaleProviderConfig{ClientID: settings.ClientID, ClientSecret: settings.ClientSecret, Tags: settings.Tag}
}

// tsdproxyConfigFile mirrors the subset of tsdproxy.yaml's schema
// (internal/config/config.go in TSDProxy v2.3.4) Once ever sets. Only the
// fields Once writes are modeled; TSDProxy defaults everything else.
type tsdproxyConfigFile struct {
	DefaultProxyProvider string                                  `yaml:"defaultProxyProvider"`
	Docker               map[string]tsdproxyDockerProviderConfig `yaml:"docker"`
	Tailscale            tsdproxyTailscaleSection                `yaml:"tailscale"`
	HTTP                 tsdproxyHTTPConfig                      `yaml:"http"`
	AdminAllowLocalhost  bool                                    `yaml:"adminAllowLocalhost"`
}

type tsdproxyDockerProviderConfig struct {
	Host                 string `yaml:"host"`
	TargetHostname       string `yaml:"targetHostname"`
	DefaultProxyProvider string `yaml:"defaultProxyProvider"`
}

type tsdproxyTailscaleSection struct {
	Providers map[string]tsdproxyTailscaleProviderConfig `yaml:"providers"`
	DataDir   string                                     `yaml:"dataDir"`
}

type tsdproxyTailscaleProviderConfig struct {
	ClientID     string `yaml:"clientId,omitempty"`
	ClientSecret string `yaml:"clientSecret,omitempty"`
	AuthKey      string `yaml:"authKey,omitempty"`
	Tags         string `yaml:"tags,omitempty"`
	ControlURL   string `yaml:"controlUrl,omitempty"`
}

type tsdproxyHTTPConfig struct {
	Hostname string `yaml:"hostname"`
	Port     int    `yaml:"port"`
}

// validateNodesCredentials proves the OAuth client can do everything TSDProxy
// will need at runtime for every app node, using the same request shape it
// sends (tailscale/provider.go's getOAuth, TSDProxy v2.3.4): mint (and
// delete) a non-reusable, preauthorized, tagged key exactly as TSDProxy mints
// one per app node, then prove the devices list is readable (the devices:core
// scope Once's own device-collision check and best-effort cleanup both need)
// and so is the Services list (the services:read scope the deploy preflight
// needs to see a Tailscale Service holding a Host).
func validateNodesCredentials(ctx context.Context, api tailscaleAPI, settings TailscaleSettings) error {
	if settings.Tag == "" {
		return fmt.Errorf("a Tailscale tag is required (e.g. tag:once); TSDProxy cannot mint per-app auth keys without one")
	}

	keyID, _, err := mintTSDProxyProbeKey(ctx, api, settings.Tag)
	if err != nil {
		return err
	}
	// Best-effort cleanup: the auth_keys scope and tag ownership are already
	// proven, so a failed delete must not fail the enable.
	_ = deleteProbeKey(ctx, api, keyID)

	if err := probeDeviceList(ctx, api); err != nil {
		return err
	}
	return probeServiceList(ctx, api)
}

// mintTSDProxyProbeKey mints a key with the exact capabilities TSDProxy
// requests for each app node at runtime: non-reusable (one key per node,
// unlike Docktail's reusable sidecar key), non-ephemeral (app nodes keep
// their tsnet state on the data volume), preauthorized, tagged. Reuses
// tsKeyRequest, the byte-compatible mirror of the Tailscale API's create-key
// request shape createTailscaleAuthKey already defines for Docktail's own
// mint - the JSON shape is the same request either topology's provider
// sends; only the field values (Reusable here, Description "tsdproxy") differ.
func mintTSDProxyProbeKey(ctx context.Context, api tailscaleAPI, tag string) (id, key string, err error) {
	var reqBody tsKeyRequest
	reqBody.Description = "tsdproxy"
	reqBody.Capabilities.Devices.Create.Reusable = false
	reqBody.Capabilities.Devices.Create.Ephemeral = false
	reqBody.Capabilities.Devices.Create.Preauthorized = true
	reqBody.Capabilities.Devices.Create.Tags = strings.Split(tag, ",")

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", err
	}

	status, body, err := api.Call(ctx, http.MethodPost, "/tailnet/-/keys", payload)
	if err != nil {
		return "", "", err
	}
	switch {
	case status == http.StatusForbidden:
		return "", "", fmt.Errorf("the OAuth client is missing the auth_keys scope (Keys -> Auth Keys: Write), needed for TSDProxy to mint each app node's auth key (HTTP %d): %s", status, errorMessage(body))
	case status == http.StatusBadRequest:
		return "", "", fmt.Errorf("Tailscale refused to mint a TSDProxy auth key for tag %q (HTTP %d): %s; %w", tag, status, errorMessage(body), ErrTagNotInPolicy)
	case status != http.StatusOK:
		return "", "", fmt.Errorf("Tailscale refused to mint a TSDProxy auth key for tag %q (HTTP %d): %s", tag, status, errorMessage(body))
	}

	var respBody struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal(body, &respBody); err != nil {
		return "", "", fmt.Errorf("decoding Tailscale key response: %w", err)
	}
	return respBody.ID, respBody.Key, nil
}

// probeDeviceList proves the devices:core scope is readable, the request
// nodesTopology's own device-collision check and best-effort teardown
// cleanup both depend on.
func probeDeviceList(ctx context.Context, api tailscaleAPI) error {
	status, body, err := api.Call(ctx, http.MethodGet, "/tailnet/-/devices", nil)
	if err != nil {
		return err
	}
	switch {
	case status == http.StatusForbidden:
		return fmt.Errorf("the OAuth client is missing the devices scope (Devices -> Core: Write), needed to check and clean up per-app tailnet devices (HTTP %d): %s", status, errorMessage(body))
	case status != http.StatusOK:
		return fmt.Errorf("listing the tailnet's devices (HTTP %d): %s", status, errorMessage(body))
	}
	return nil
}

// probeServiceList proves the services:read scope with the request Docktail's
// listServices sends.
func probeServiceList(ctx context.Context, api tailscaleAPI) error {
	status, body, err := api.Call(ctx, http.MethodGet, servicesPath, nil)
	if err != nil {
		return err
	}
	switch {
	case status == http.StatusForbidden:
		return fmt.Errorf("the OAuth client is missing the services:read scope (General -> Services: Read), needed to refuse a Host a Tailscale Service already holds (HTTP %d): %s; %s", status, errorMessage(body), remintCredentialHint)
	case status != http.StatusOK:
		return fmt.Errorf("listing the tailnet's Services (HTTP %d): %s", status, errorMessage(body))
	}
	return nil
}

// tailscaleDevice is the subset of the SaaS devices-list response Once reads:
// the preferred node identifier (client/tailscale/v2's Device.NodeID, JSON
// "nodeId"), its Magic DNS short hostname, and its ACL tags.
type tailscaleDevice struct {
	NodeID   string   `json:"nodeId"`
	Hostname string   `json:"hostname"`
	Tags     []string `json:"tags"`
}

// listTailscaleDevices lists every device in the OAuth client's tailnet via
// GET /tailnet/-/devices, the same endpoint validateHostInTailnet already
// uses.
func listTailscaleDevices(ctx context.Context, api tailscaleAPI) ([]tailscaleDevice, error) {
	status, body, err := api.Call(ctx, http.MethodGet, "/tailnet/-/devices", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("listing the tailnet's devices (HTTP %d): %s", status, errorMessage(body))
	}
	var resp struct {
		Devices []tailscaleDevice `json:"devices"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decoding the tailnet devices list: %w", err)
	}
	return resp.Devices, nil
}

// refuseIfDeviceExists is nodes' deploy-time collision guard (ADR-0015): a
// Host that already names a device in the tailnet is refused, naming the
// device, rather than letting TSDProxy fall back to a "-1" alias Once's URL
// model does not expect. A colliding device that carries Once's own tag is
// the leftover of a failed or removed deploy - the caller already checked
// TSDProxy's live state, so no running node backs it - and is deleted
// instead, or the name would stay blocked forever. Best effort in the other
// direction - an unreadable devices list must not block a deploy that
// credentials already proved could mint a key; only a definite collision
// refuses.
func refuseIfDeviceExists(ctx context.Context, api tailscaleAPI, name, tag string) error {
	devices, err := listTailscaleDevices(ctx, api)
	if err != nil {
		return nil
	}
	var tags []string
	if tag != "" {
		tags = policyTags(tag)
	}
	for _, device := range devices {
		if device.Hostname != name {
			continue
		}
		if hasAnyTag(device.Tags, tags) {
			if err := deleteDevice(ctx, api, device.NodeID); err != nil {
				return fmt.Errorf("a tailnet device named %q is a leftover of a previous %q deploy, but sweeping it failed: %w; remove the device by hand or rename this app", name, name, err)
			}
			continue
		}
		return fmt.Errorf("a tailnet device named %q already exists (node %s); rename this app or remove that device first - TSDProxy would otherwise register it as %q", name, device.NodeID, name+"-1")
	}
	return nil
}

// refuseIfServiceHoldsName extends the collision guard past devices: a
// Tailscale Service holds a Magic DNS name too, and never shows in the
// devices list, so svc:<name> would silently turn this app's node into
// <name>-1. Refused, naming the Service - one carrying Once's tag is named as
// a leftover of the services topology, from an install that predates
// servicesTopology.leave. Best effort like the device check: a credential
// without the services scope, or an unreachable API, skips it - the former
// reported as servicesScopeMissing, since it leaves a holder Once cannot see.
func refuseIfServiceHoldsName(ctx context.Context, api tailscaleAPI, name, tag string) (servicesScopeMissing bool, err error) {
	svcName := "svc:" + name
	svc, found, err := getTailscaleService(ctx, api, svcName)
	if err != nil || !found {
		return errors.Is(err, errServicesUnreadable), nil
	}
	holder := "the Tailscale Service " + svcName
	if hasAnyTag(svc.Tags, policyTags(tag)) {
		holder += ", left over from Once's services topology,"
	}
	return false, fmt.Errorf("%s holds the name %q; delete it in the admin console (Services) or rename this app - TSDProxy would otherwise register it as %q", holder, name, name+"-1")
}

// refuseRenamedNode compares the name a node asked for with the leading label
// of the FQDN it joined under, and diagnoses a mismatch: Magic DNS handed the
// node the next free variant because something else holds the name, and that
// suffix is permanent for the node. The holder is looked for among Services,
// since a device holder would have been refused by validateHost; a credential
// that cannot read them is named as the likely reason the holder went unseen.
// api is nil on the Control Seam, which has no Services.
func refuseRenamedNode(ctx context.Context, api tailscaleAPI, name, fqdn string) error {
	got := nodeLabel(fqdn)
	if got == name {
		return nil
	}

	cause := fmt.Sprintf("it was most likely claimed between Once's check and the node joining, or a device Once just swept had not released it yet; look for %q in the admin console (Machines and Services), then deploy again", name)
	if api != nil {
		_, found, err := getTailscaleService(ctx, api, "svc:"+name)
		switch {
		case errors.Is(err, errServicesUnreadable):
			cause = fmt.Sprintf("the Tailscale credential lacks the services:read scope (General -> Services: Read), so a Tailscale Service holding it went unseen; look for svc:%s in the admin console (Services), delete it or rename this app, and replace the credential - %s", name, remintCredentialHint)
		case found:
			cause = fmt.Sprintf("the Tailscale Service svc:%s holds it; delete it in the admin console (Services) or rename this app, then deploy again", name)
		}
	}
	return &nodeRenamedError{requested: name, joined: got, fqdn: fqdn, cause: cause}
}

// nodeRenamedError is a node that joined the tailnet under a name other than
// the one requested, with the likely cause and its fix.
type nodeRenamedError struct {
	requested string
	joined    string
	fqdn      string
	cause     string
}

func (e *nodeRenamedError) Error() string {
	return fmt.Sprintf("the tailnet node for %q joined as %q (%s), because something else already holds %q: %s. The node keeps %q until its device is deleted (admin console, Machines); Once sweeps an app's on its next deploy", e.requested, e.joined, e.fqdn, e.requested, e.cause, e.joined)
}

// nodeLabel is the leading DNS label of a node's FQDN - the name Magic DNS
// gave it - compared whole, so no prefix passes for a match.
func nodeLabel(fqdn string) string {
	label, _, _ := strings.Cut(fqdn, ".")
	return label
}

// deleteDevice deletes one tailnet device row. A device that is already gone
// counts as deleted - the sweeps this backs are reconciling toward absence,
// and racing an admin-console removal must not turn success into an error.
func deleteDevice(ctx context.Context, api tailscaleAPI, nodeID string) error {
	status, body, err := api.Call(ctx, http.MethodDelete, "/device/"+url.PathEscape(nodeID), nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusNotFound {
		return fmt.Errorf("deleting device %s (HTTP %d): %s", nodeID, status, errorMessage(body))
	}
	return nil
}

// deleteTaggedDevices is bestEffortDeleteTaggedDevices' live sweep, factored
// out so it is testable against a fake tailscaleAPI without a real Docker
// daemon or Tailscale API - remove and destroy's only live behaviour that
// does not touch the engine.
func deleteTaggedDevices(ctx context.Context, api tailscaleAPI, tag, hostname string) {
	devices, err := listTailscaleDevices(ctx, api)
	if err != nil {
		return
	}
	tags := policyTags(tag)
	for _, device := range devices {
		if hostname != "" && device.Hostname != hostname {
			continue
		}
		if !hasAnyTag(device.Tags, tags) {
			continue
		}
		_ = deleteDevice(ctx, api, device.NodeID)
	}
}

func hasAnyTag(have, tags []string) bool {
	for _, t := range tags {
		if slices.Contains(have, t) {
			return true
		}
	}
	return false
}

// waitForNodeFQDN polls TSDProxy until the named app's node reports the
// tailnet FQDN it answers on. Read, never composed from the name and the
// Magic DNS suffix: Tailscale hands a deduped "name-1" to a node whose
// requested hostname is taken - which Once's own leftover-device sweep
// provokes, since deleting the predecessor does not free the name in time for
// the replacement - and TSDProxy goes on calling the proxy by the requested
// name while forwarding the assigned one as the Host. A composed route then
// matches nothing TSDProxy ever sends, so every tailnet request answers 404
// against a healthy container, and nothing re-registers the route afterwards.
// A first registration waits, because a node is routinely still joining when
// its app's route is registered; a re-registration passes a zero timeout,
// which reads once. A node that never reports within the wait is an error
// here, which nodeRouteHost turns into its composed-Host fallback rather than
// a failed deploy: TSDProxy drops a proxy from its API entirely while its
// container is replaced, so a silent node is the routine case on a Roll.
func waitForNodeFQDN(ctx context.Context, reader TailnetReader, name string, timeout, poll time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		status, err := reader.ServeStatus(ctx)
		if err == nil {
			if fqdn := status.FQDNs[name]; fqdn != "" {
				return fqdn, nil
			}
		}
		lastErr = err
		if !time.Now().Before(deadline) {
			if lastErr != nil {
				return "", fmt.Errorf("reading the tailnet host %q answers on: %w", name, lastErr)
			}
			return "", fmt.Errorf("the node for %q has not reported a tailnet host yet, so there is nothing to route to it; check that it joined (`once tailscale status`) and deploy again", name)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(poll):
		}
	}
}

// nodeRouteHost resolves the Host a nodes route answers on: the FQDN the
// node reports within fqdnTimeout, else the name composed with the tailnet's
// Magic DNS suffix. See Tailscale.NodeRouteHost for why the read is preferred
// but not required.
func nodeRouteHost(ctx context.Context, reader TailnetReader, name string, fqdnTimeout, suffixTimeout, poll time.Duration) (string, error) {
	fqdn, fqdnErr := waitForNodeFQDN(ctx, reader, name, fqdnTimeout, poll)
	if fqdnErr == nil {
		return fqdn, nil
	}
	if ctx.Err() != nil {
		return "", fqdnErr
	}

	suffix, err := waitForMagicDNSSuffix(ctx, reader, suffixTimeout, poll)
	if err != nil || suffix == "" {
		return "", fmt.Errorf("no tailnet host to route %q on: %w", name, fqdnErr)
	}
	slog.Warn("Node reported no tailnet host; routing on the composed name until a proxy roll re-resolves it",
		"app", name, "host", name+"."+suffix, "reason", fqdnErr)
	return name + "." + suffix, nil
}

// joinNodeRouteHost is nodeRouteHost for a first registration, the one
// moment a deduped name can be caught: the node is waited for, and one that
// joined under a name other than the requested one is refused (nodeRenamedError)
// rather than routed. A composed fallback carries the requested name, so it
// passes the comparison and is left for a proxy roll to heal.
func joinNodeRouteHost(ctx context.Context, reader TailnetReader, api tailscaleAPI, name string, fqdnTimeout, suffixTimeout, poll time.Duration) (string, error) {
	host, err := nodeRouteHost(ctx, reader, name, fqdnTimeout, suffixTimeout, poll)
	if err != nil {
		return "", err
	}
	if err := refuseRenamedNode(ctx, api, name, host); err != nil {
		return "", err
	}
	return host, nil
}

// registerNodesProxyRoute registers the Proxy route a nodes-topology app (or
// once-admin) answers on: the tailnet FQDN its own TSDProxy node reports,
// since that is what TSDProxy forwards as the Host. joined is the Host the
// caller already resolved for a node that has joined before (a Roll, a
// re-issue); empty means the node joins with this registration, so the Host
// comes from Tailscale.JoinNodeRouteHost, which refuses a deduped name. TLS
// is off: TSDProxy already terminated it with the tailnet's own certificate before forwarding here. Shared by application.go
// and admin.go, the two callers that create a container tailnet apps answer
// through under nodes. The route answers only a Trusted Source (ADR-0016):
// tailnet traffic arrives from the TSDProxy node, a container on the once
// network, so the network's subnets admit it while an outsider spoofing the
// tailnet Host gets an unknown-Host 404.
func registerNodesProxyRoute(ctx context.Context, ns *Namespace, appName, host, containerID, healthCheckPath, joined string) error {
	routeHost := joined
	if routeHost == "" {
		var err error
		routeHost, err = ns.Tailscale().JoinNodeRouteHost(ctx, host)
		if err != nil {
			return err
		}
	} else if nodeLabel(routeHost) != host {
		slog.Warn("Node answers on a name other than the requested Host; routing on the name it holds",
			"app", appName, "requested", host, "host", routeHost)
	}

	subnets, err := ns.NetworkSubnets(ctx)
	if err != nil {
		return fmt.Errorf("resolving trusted sources: %w", err)
	}
	if err := ns.Proxy().Deploy(ctx, DeployOptions{
		AppName:         appName,
		Target:          containerID[:12],
		Host:            routeHost,
		TLS:             false,
		HealthCheckPath: healthCheckPath,
		AllowFrom:       subnets,
	}); err != nil {
		return fmt.Errorf("registering with proxy: %w", err)
	}
	return nil
}
