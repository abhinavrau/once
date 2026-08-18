package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Images are pinned to exact versions, the same doctrine as proxyImage.
// Bumped via Once releases through the self-update mechanism. The tailscale
// image must be v1.92.1+: Services exist from v1.86, but docktail proxies
// service traffic straight to container IPs, and serve only accepts
// non-localhost targets from v1.92.1 (older daemons reject them with "only
// localhost or 127.0.0.1 proxies are currently supported").
const (
	docktailImage         = "ghcr.io/marvinvr/docktail:1.5.0"
	tailscaleSidecarImage = "tailscale/tailscale:v1.98.9"
)

// Paths shared between tailscaled and docktail. The socket directory is
// mounted into once-docktail either from the host (host mode) or from the
// sidecar's socket volume (sidecar mode); mounting the directory rather than
// the socket file keeps the mount valid across tailscaled restarts.
const (
	tailscaledSocketDir  = "/var/run/tailscale"
	tailscaledSocketFile = "tailscaled.sock"
	sidecarStateDir      = "/var/lib/tailscale"
)

// sidecarLogin bounds the post-boot wait for the sidecar's tailscaled to
// register and log in (backend state Running). Only a crash-looping container
// fails it; a merely slow login times out non-fatally and the suffix and
// verify waits pick it up from there.
const (
	sidecarLoginTimeout = 30 * time.Second
	sidecarLoginPoll    = time.Second
)

// suffixWait bounds the post-boot wait for tailscaled to join the control
// plane and report the Magic DNS suffix. Non-fatal on timeout: DomainSuffix()
// lazily reads and persists it later, so a slow tailnet never fails enable.
const (
	suffixWaitTimeout = 20 * time.Second
	suffixWaitPoll    = 2 * time.Second
)

// serviceVerify bounds the post-enable wait for every expected service to be
// advertised by the local tailscaled and approved in the tailnet. Docktail
// reconciles labels within seconds; the bound covers slow cloud propagation.
const (
	serviceVerifyTimeout = 90 * time.Second
	serviceVerifyPoll    = 2 * time.Second
)

// funnelPublicPorts are the public ports Tailscale Funnel can serve HTTPS on.
var funnelPublicPorts = []int{443, 8443, 10000}

// dnsLabelPattern is an RFC 1123 DNS label: what a service name must be to be
// reachable as <name>.<tailnet-suffix> once advertised as a Tailscale Service.
var dnsLabelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// ErrTagNotInPolicy marks enable failures that are fixed by merging Once's
// snippet into the tailnet policy, so callers can print it alongside the
// error. Only 400 responses carry it: that is how the API rejects an
// undefined or unpermitted tag, while 429s and 5xxs mean something else.
var ErrTagNotInPolicy = errors.New("define the tag in your tailnet policy (Access Controls -> tagOwners) first")

const (
	// probeServiceName is the throwaway Service definition created and deleted by
	// the enable-time probe. probeServiceComment marks it as Once's, so a leftover
	// from an interrupted probe is recognized and reused instead of blocking enable.
	probeServiceName    = "svc:once-probe"
	probeServiceComment = "Once credential probe - safe to delete"
)

// minHostDaemonMajor/Minor is the oldest host tailscaled that supports
// Tailscale Services with the remote (container IP) serve targets docktail
// uses; older daemons reject them with "only localhost or 127.0.0.1 proxies
// are currently supported".
const (
	minHostDaemonMajor = 1
	minHostDaemonMinor = 92
)

// servicesSetupIntro is the Services topology's setup prose, shown before
// enable via TopologyFacts.SetupIntro (ticket 05).
const servicesSetupIntro = `Setup, once per tailnet:
 1. Run 'once tailscale policy' and merge the printed snippet into your
    tailnet policy file (https://login.tailscale.com/admin/acls).
 2. In the admin console (Settings -> Trust credentials), create an OAuth
    client with write access to Auth Keys, Devices Core, and Services,
    tagged with your tag (default tag:once).`

// servicesTopology is the Services topology's adapter (ADR-0002 behind the
// seam): one shared tailnet node, driven by once-docktail, advertises a
// Tailscale Service per app.
type servicesTopology struct {
	namespace *Namespace
	// awaitLogin is a hook for the post-boot sidecar login wait, which runs
	// the tailscale CLI over a hijacked docker exec connection and so is
	// unfakeable at the boot dispatch's client seam (ADR-0009). Nil means the
	// real wait; only tests override it.
	awaitLogin func(context.Context) error
}

func newServicesTopology(ns *Namespace) *servicesTopology {
	return &servicesTopology{namespace: ns}
}

// boot idempotently ensures once-docktail - and, in sidecar mode, the
// once-tailscale sidecar it drives over a shared socket volume (userspace
// networking, unprivileged) - is running with settings. settings.Mode must
// already be resolved (probe does that); boot does no mode defaulting of its
// own.
func (s *servicesTopology) boot(ctx context.Context, settings TailscaleSettings) error {
	info, err := s.namespace.client.ContainerInspect(ctx, s.containerName())
	switch {
	case err == nil:
		if settings.Mode == TailscaleModeSidecar {
			if err := s.bootSidecar(ctx, settings); err != nil {
				return err
			}
		}
		if !info.State.Running {
			if err := s.namespace.client.ContainerStart(ctx, info.ID, container.StartOptions{}); err != nil {
				return fmt.Errorf("starting docktail container: %w", err)
			}
		}
		return nil
	case !errdefs.IsNotFound(err):
		return fmt.Errorf("inspecting docktail container: %w", err)
	}

	if settings.Mode == TailscaleModeSidecar {
		if err := s.bootSidecar(ctx, settings); err != nil {
			return err
		}
	}

	// Bake the suffix into the settings label now so the container is
	// created once, not created and immediately recreated by a persist.
	if settings.DomainSuffix == "" {
		settings.DomainSuffix, _ = waitForMagicDNSSuffix(ctx, s.reader(settings))
	}

	if err := s.pullImage(ctx, docktailImage); err != nil {
		return err
	}
	return s.create(ctx, settings)
}

// remove removes once-docktail, the Once-managed sidecar, and the transient
// socket volume, keeping the sidecar's state volume (node identity). settings
// is unused: Services has no per-app device to best-effort delete when it is
// left, unlike nodes.
func (s *servicesTopology) remove(ctx context.Context, settings TailscaleSettings) error {
	if err := s.removeContainer(ctx, s.containerName()); err != nil {
		return err
	}
	if err := s.removeContainer(ctx, s.sidecarName()); err != nil {
		return err
	}
	return s.removeVolume(ctx, s.socketVolumeName())
}

// destroy removes everything remove does, plus the sidecar's state volume -
// full cleanup, used by teardown.
func (s *servicesTopology) destroy(ctx context.Context, settings TailscaleSettings) error {
	if err := s.remove(ctx, settings); err != nil {
		return err
	}
	return s.removeVolume(ctx, s.stateVolumeName())
}

// relabel rewrites once-docktail's settings label by recreating it (Docker
// labels are immutable on a live container). The sidecar is untouched - used
// to bake in a newly-learned domain suffix or node host.
func (s *servicesTopology) relabel(ctx context.Context, settings TailscaleSettings) error {
	if err := s.removeContainer(ctx, s.containerName()); err != nil {
		return err
	}
	return s.create(ctx, settings)
}

// appLabels returns the labels that expose an app as a Tailscale Service
// under its own name. The port-80 backend matches Once's port-80 assumption;
// the https service protocol makes the tailnet side serve on 443, matching
// the https:// URLs Once displays; network pins direct container-IP proxying
// to the namespace network the app is attached to.
//
// When funnelPort is set, the same backend is also exposed to the public
// internet via docktail.funnel.* (https on that public port). Each app gets
// its own public port because Funnel serves one endpoint per port per node.
func (s *servicesTopology) appLabels(host, network string, funnelPort int) map[string]string {
	labels := map[string]string{
		"docktail.service.enable":           "true",
		"docktail.service.name":             host,
		"docktail.service.port":             "80",
		"docktail.service.service-protocol": "https",
		"docktail.service.network":          network,
	}
	if funnelPort != 0 {
		labels["docktail.funnel.enable"] = "true"
		labels["docktail.funnel.port"] = "80"
		labels["docktail.funnel.funnel-port"] = strconv.Itoa(funnelPort)
	}
	return labels
}

// probe is the enable-time credential/preflight check: resolve the requested
// mode (ADR-0007), run the host-mode pre-flight when resolved to host
// (ADR-0007's four hard checks that fail within seconds naming the fix), then
// prove the OAuth credentials before anything boots. It returns settings with
// Mode resolved, and the --mode host discoverability notice (or "").
func (s *servicesTopology) probe(ctx context.Context, settings TailscaleSettings) (TailscaleSettings, string, error) {
	resolved, fresh, err := s.resolveMode(ctx, settings.Mode)
	if err != nil {
		return TailscaleSettings{}, "", err
	}
	notice := hostModeNotice(settings.Mode, fresh)
	settings.Mode = resolved

	if settings.Mode == TailscaleModeHost {
		if err := validateHostModePreflight(ctx, newTailscaleAPI(settings), s.reader(settings), settings); err != nil {
			return TailscaleSettings{}, "", err
		}
	}
	if err := validateTailscaleCredentials(ctx, settings); err != nil {
		return TailscaleSettings{}, "", err
	}
	return settings, notice, nil
}

// reader returns the TailnetReader matching where tailscaled runs: the host
// CLI in host mode, docker exec into the sidecar otherwise.
func (s *servicesTopology) reader(settings TailscaleSettings) TailnetReader {
	if settings.Mode == TailscaleModeHost {
		return NewHostTailnetReader()
	}
	return NewSidecarTailnetReader(s.namespace.client, s.sidecarName())
}

// verify confirms each expected service is reachable-in-principle after
// enable: advertised according to the local daemon's serve status, and
// approved according to the Tailscale cloud API - authoritative, since local
// state can lag (tailscale/tailscale#18821). Skipped entirely on the
// headscale control seam: Services do not exist on headscale.
func (s *servicesTopology) verify(ctx context.Context, settings TailscaleSettings, expected []string, progress DeployProgressCallback) error {
	if controlSeamActive() {
		return nil
	}

	reader := s.reader(settings)
	if err := requireServeEnabled(ctx, reader); err != nil {
		return err
	}

	announce := func(line string) {
		if progress != nil {
			progress(DeployProgress{Stage: DeployStageWaiting, Status: line})
		}
	}
	hint := s.logsHint()
	if err := waitForAdvertised(ctx, reader, expected, hint, serviceVerifyTimeout, serviceVerifyPoll, announce); err != nil {
		return err
	}
	return waitForApproved(ctx, newTailscaleAPI(settings), settings, expected, hint, serviceVerifyTimeout, serviceVerifyPoll, announce)
}

// status reports each named service's current state: advertised according to
// the local daemon's serve status, and approved according to the Tailscale
// cloud API. On the headscale control seam only advertisement is checked -
// the cloud endpoints are Tailscale SaaS only.
func (s *servicesTopology) status(ctx context.Context, settings TailscaleSettings, names []string) ([]ServiceStatus, error) {
	return serviceStatuses(ctx, s.reader(settings), newTailscaleAPI(settings), settings, names, s.logsHint())
}

// funnelPort picks the public port for self's Funnel: the port it already
// holds while active (a re-enable must not move a live URL), else the first
// free one. Funnel serves one endpoint per public port per node, so a port
// may back only one app at a time.
func (s *servicesTopology) funnelPort(self *Application, apps []*Application) (int, error) {
	used := map[int]bool{}
	for _, app := range apps {
		if app == self {
			continue
		}
		if p := app.Settings.ActiveFunnelPort(); p != 0 {
			used[p] = true
		}
	}
	if p := self.Settings.ActiveFunnelPort(); p != 0 && !used[p] {
		return p, nil
	}
	for _, p := range funnelPublicPorts {
		if !used[p] {
			return p, nil
		}
	}
	return 0, ErrFunnelPortsExhausted
}

// validateHost rejects a name that cannot be a Magic DNS label, since
// Docktail would advertise it as <name>.<suffix> and the entry could never
// resolve. ctx and settings are unused: Services has no live device check.
func (s *servicesTopology) validateHost(ctx context.Context, settings TailscaleSettings, name string) error {
	return ValidateTailscaleServiceName(name)
}

// logsHint names the container whose logs explain a tailnet read that came
// back short - the one source every such message takes it from.
func (s *servicesTopology) logsHint() string {
	return "the " + s.containerName() + " container logs"
}

// facts returns the Services topology's static, context-free data.
func (s *servicesTopology) facts() TopologyFacts {
	return TopologyFacts{
		DefaultSettings: TailscaleSettings{Tag: "tag:once", Mode: TailscaleModeSidecar, Topology: TailnetTopologyServices},
		OAuthScopes:     []string{"auth_keys", "devices:core", "services"},
		PolicySnippet:   PolicySnippet,
		ModeChoices:     []string{TailscaleModeSidecar, TailscaleModeHost},
		SetupIntro:      servicesSetupIntro,
	}
}

// ValidateTailscaleServiceName rejects a name that cannot be a Magic DNS
// label, since Docktail would advertise it as <name>.<suffix> and the entry
// could never resolve.
func ValidateTailscaleServiceName(name string) error {
	if len(name) > 63 || !dnsLabelPattern.MatchString(name) {
		return fmt.Errorf("service name %q is not a valid DNS label: use 1-63 lowercase letters, digits, or hyphens (not at the start or end)", name)
	}
	return nil
}

// Private

func (s *servicesTopology) containerName() string {
	return s.namespace.name + "-docktail"
}

func (s *servicesTopology) sidecarName() string {
	return s.namespace.name + "-tailscale"
}

func (s *servicesTopology) stateVolumeName() string {
	return s.namespace.name + "-tailscale-state"
}

func (s *servicesTopology) socketVolumeName() string {
	return s.namespace.name + "-tailscale-socket"
}

// resolveMode is the only implementation of the ADR-0007 mode rule: an
// explicit mode wins, an empty one keeps an already-enabled install's stored
// mode, a stored mode that is missing or unrecognized (a corrupt or legacy
// label) reads as the sidecar, and so does a fresh enable. A real inspect
// failure is an error, not a silent sidecar default: the resolved mode decides
// whether the host-mode pre-flight must run, and guessing wrong would skip it.
//
// fresh reports that no docktail container exists yet, which feeds the
// --mode host notice. It is only determined when no mode was requested: an
// explicit mode needs no inspect, and the notice it would feed never fires.
func (s *servicesTopology) resolveMode(ctx context.Context, requested string) (string, bool, error) {
	if requested != "" {
		return requested, false, nil
	}
	info, err := s.namespace.client.ContainerInspect(ctx, s.containerName())
	if errdefs.IsNotFound(err) {
		return TailscaleModeSidecar, true, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("inspecting docktail container: %w", err)
	}
	stored, _ := UnmarshalTailscaleSettings(info.Config.Labels[labelKey])
	if stored.Mode != "" && ValidateTailscaleMode(stored.Mode) == nil {
		return stored.Mode, false, nil
	}
	return TailscaleModeSidecar, false, nil
}

// sidecarBootClient lists the engine methods the sidecar boot dispatch calls,
// so the dispatch is drivable by a fake without a daemon (ADR-0009). The
// concrete *client.Client satisfies it structurally, so production passes it
// unchanged.
type sidecarBootClient interface {
	containerRemover
	imagePuller
	ContainerInspect(ctx context.Context, containerID string) (container.InspectResponse, error)
	ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error)
	ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error
}

// containerRemover and imagePuller keep the shared remove and pull helpers down
// to the one method each of them calls, so callers outside the boot dispatch
// don't inherit the whole seam.
type containerRemover interface {
	ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error
}

type imagePuller interface {
	ImagePull(ctx context.Context, ref string, options image.PullOptions) (io.ReadCloser, error)
}

// bootSidecar idempotently boots the once-tailscale sidecar: an unprivileged
// tailscale/tailscale container in userspace networking mode - no TUN device,
// no NET_ADMIN, no host networking - with its node state on a volume and its
// socket shared with docktail via another. On the SaaS path its auth key is
// minted with the same shape the enable-time probe proved; on the control seam
// the pre-minted key and control URL are used instead. Booting includes the
// login wait: a sidecar that crash-loops instead of joining fails here, naming
// the real problem, rather than much later in the service verify. A sidecar
// found already crash-looping is rebuilt with a freshly minted key.
func (s *servicesTopology) bootSidecar(ctx context.Context, settings TailscaleSettings) error {
	return s.bootSidecarWithClient(ctx, s.namespace.client, settings)
}

// bootSidecarWithClient is bootSidecar's dispatch, reaching the engine by
// parameter rather than through the namespace, so tests drive every branch -
// crash-looping, stopped, running, missing - with a fake.
func (s *servicesTopology) bootSidecarWithClient(ctx context.Context, c sidecarBootClient, settings TailscaleSettings) error {
	info, err := c.ContainerInspect(ctx, s.sidecarName())
	switch {
	case err == nil && sidecarWedged(info.State):
		// A crash-looping sidecar can't be started out of its loop: its baked-in
		// TS_AUTHKEY may be dead (revoked, expired). Rebuild it with a freshly
		// minted key - the state volume survives, so the node identity does too.
		if err := forceRemoveContainer(ctx, c, s.sidecarName()); err != nil {
			return err
		}
	case err == nil:
		if !info.State.Running {
			if err := c.ContainerStart(ctx, info.ID, container.StartOptions{}); err != nil {
				return fmt.Errorf("starting tailscale sidecar: %w", err)
			}
		}
		return s.awaitSidecarLogin(ctx)
	case !errdefs.IsNotFound(err):
		return fmt.Errorf("inspecting tailscale sidecar: %w", err)
	}

	authKey, controlURL, seam := controlSeam()
	var extraArgs string
	if seam {
		extraArgs = "--login-server=" + controlURL
	} else {
		if authKey, err = mintSidecarAuthKey(ctx, newTailscaleAPI(settings), settings); err != nil {
			return err
		}
		extraArgs = "--advertise-tags=" + settings.Tag
	}

	if err := pullImageRef(ctx, c, tailscaleSidecarImage); err != nil {
		return err
	}

	resp, err := c.ContainerCreate(ctx,
		&container.Config{
			Image: tailscaleSidecarImage,
			Env: []string{
				"TS_AUTHKEY=" + authKey,
				"TS_STATE_DIR=" + sidecarStateDir,
				"TS_SOCKET=" + tailscaledSocketDir + "/" + tailscaledSocketFile,
				"TS_USERSPACE=true",
				"TS_HOSTNAME=" + s.sidecarName(),
				"TS_EXTRA_ARGS=" + extraArgs,
			},
		},
		&container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyAlways},
			LogConfig:     ContainerLogConfig(),
			Mounts: []mount.Mount{
				{Type: mount.TypeVolume, Source: s.stateVolumeName(), Target: sidecarStateDir},
				{Type: mount.TypeVolume, Source: s.socketVolumeName(), Target: tailscaledSocketDir},
			},
		},
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				s.namespace.name: {},
			},
		},
		nil,
		s.sidecarName(),
	)
	if err != nil {
		return fmt.Errorf("creating tailscale sidecar: %w", err)
	}

	if err := c.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		c.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return fmt.Errorf("starting tailscale sidecar: %w", err)
	}
	return s.awaitSidecarLogin(ctx)
}

func (s *servicesTopology) awaitSidecarLogin(ctx context.Context) error {
	if s.awaitLogin != nil {
		return s.awaitLogin(ctx)
	}
	reader := NewSidecarTailnetReader(s.namespace.client, s.sidecarName())
	return waitForSidecarLogin(ctx, reader, s.sidecarCrashed, sidecarLoginTimeout, sidecarLoginPoll)
}

// sidecarCrashed reports whether the sidecar container has died or is
// restart-looping, with a message quoting its recent logs - a rejected
// TS_AUTHKEY presents exactly this way. Inspect failures read as "not
// crashed"; the login wait's daemon probe surfaces those.
func (s *servicesTopology) sidecarCrashed(ctx context.Context) (string, bool) {
	info, err := s.namespace.client.ContainerInspect(ctx, s.sidecarName())
	if err != nil || !sidecarWedged(info.State) {
		return "", false
	}
	msg := fmt.Sprintf("the %s sidecar keeps exiting (exit code %d) instead of joining the tailnet", s.sidecarName(), info.State.ExitCode)
	return withRecentLogs(ctx, s.namespace.client, s.sidecarName(), msg), true
}

// create boots once-docktail: Docker socket read-only, the tailscaled socket
// directory mounted from the host or the sidecar's socket volume. With OAuth
// credentials docktail also syncs Service definitions to the control plane; on
// the headscale control seam it runs in manual mode (no cloud API).
func (s *servicesTopology) create(ctx context.Context, settings TailscaleSettings) error {
	var env []string
	if !controlSeamActive() {
		env = []string{
			"TAILSCALE_OAUTH_CLIENT_ID=" + settings.ClientID,
			"TAILSCALE_OAUTH_CLIENT_SECRET=" + settings.ClientSecret,
			"DEFAULT_SERVICE_TAGS=" + settings.Tag,
		}
	}

	hostConfig := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyAlways},
		LogConfig:     ContainerLogConfig(),
		Binds:         []string{"/var/run/docker.sock:/var/run/docker.sock:ro"},
	}
	if settings.Mode == TailscaleModeHost {
		hostConfig.Binds = append(hostConfig.Binds, hostTailscaledDir()+":"+tailscaledSocketDir)
	} else {
		hostConfig.Mounts = []mount.Mount{
			{Type: mount.TypeVolume, Source: s.socketVolumeName(), Target: tailscaledSocketDir},
		}
	}

	resp, err := s.namespace.client.ContainerCreate(ctx,
		&container.Config{
			Image: docktailImage,
			Env:   env,
			Labels: map[string]string{
				labelKey: settings.Marshal(),
			},
		},
		hostConfig,
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				s.namespace.name: {},
			},
		},
		nil,
		s.containerName(),
	)
	if err != nil {
		return fmt.Errorf("creating docktail container: %w", err)
	}

	if err := s.namespace.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		s.namespace.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return fmt.Errorf("starting docktail container: %w", err)
	}
	return nil
}

func (s *servicesTopology) pullImage(ctx context.Context, ref string) error {
	return pullImageRef(ctx, s.namespace.client, ref)
}

func (s *servicesTopology) removeContainer(ctx context.Context, name string) error {
	return forceRemoveContainer(ctx, s.namespace.client, name)
}

func (s *servicesTopology) removeVolume(ctx context.Context, name string) error {
	if err := s.namespace.client.VolumeRemove(ctx, name, true); err != nil {
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("removing volume %s: %w", name, err)
		}
	}
	return nil
}

// Helpers

// validateTailscaleCredentials is the enable-time pre-flight: it proves the
// OAuth credentials work before any container is booted. Skipped on the hidden
// headscale control seam, which uses a pre-minted auth key, not OAuth.
func validateTailscaleCredentials(ctx context.Context, settings TailscaleSettings) error {
	if controlSeamActive() {
		return nil
	}
	return validateOAuthCredentials(ctx, newTailscaleAPI(settings), settings)
}

// tsKeyRequest mirrors the subset of Tailscale's CreateKeyRequest Once uses to
// mint the once-tailscale sidecar's auth key, so the probe and the real sidecar
// boot stay in lockstep.
type tsKeyRequest struct {
	Capabilities struct {
		Devices struct {
			Create struct {
				Reusable      bool     `json:"reusable"`
				Ephemeral     bool     `json:"ephemeral"`
				Preauthorized bool     `json:"preauthorized"`
				Tags          []string `json:"tags"`
			} `json:"create"`
		} `json:"devices"`
	} `json:"capabilities"`
	Description string `json:"description"`
}

// validateOAuthCredentials proves the OAuth client can do everything Docktail
// and the Once-managed sidecar will need, using the same request shapes they
// send: a token exchange (the adapter's, on the first call), a mint-and-delete
// of a sidecar-shaped auth key (auth_keys scope, tag ownership), and a
// create-inspect-delete of a throwaway Service definition (services and
// devices scopes, tag definition). Each failure mode gets its own actionable
// error - including rejected credentials, which surface from the first call,
// before any key is minted.
func validateOAuthCredentials(ctx context.Context, api tailscaleAPI, settings TailscaleSettings) error {
	if settings.Tag == "" {
		return fmt.Errorf("a Tailscale tag is required (e.g. tag:once); the sidecar auth key and Service definitions cannot be created without one")
	}

	keyID, _, err := createTailscaleAuthKey(ctx, api, settings.Tag)
	if err != nil {
		return err
	}

	// Best-effort cleanup: the auth_keys scope and tag are already proven, so a
	// failed delete must not fail the enable. ponytail: probe keys also expire on
	// Tailscale's default schedule; the delete just keeps repeated enables from
	// piling up stale keys.
	_ = deleteProbeKey(ctx, api, keyID)

	return probeServiceDefinition(ctx, api, settings.Tag)
}

// validateHostModePreflight is the host-mode counterpart of the credential
// probe (ADR-0007): four hard checks that fail within seconds naming the fix,
// instead of the enable booting the full topology and dying much later in the
// service-advertisement verify. The tailnet-membership check needs the SaaS
// devices API, so it is skipped on the headscale control seam; the local
// checks apply everywhere. The API seam and reader are parameterised so tests
// can use the canned-JSON fakes.
func validateHostModePreflight(ctx context.Context, api tailscaleAPI, reader TailnetReader, settings TailscaleSettings) error {
	if !hostTailscaledPresent() {
		return fmt.Errorf("host mode needs a running tailscaled on this host, but its socket %s is missing; install and start Tailscale, or use --mode sidecar",
			filepath.Join(hostTailscaledDir(), tailscaledSocketFile))
	}

	self, err := reader.SelfStatus(ctx)
	if err != nil {
		return fmt.Errorf("reading the host tailscaled's status: %w", err)
	}
	if missing := missingNodeTags(settings.Tag, self.Tags); len(missing) > 0 {
		return fmt.Errorf("the host node is not tagged with %s, so it cannot advertise Once's Services; run `sudo tailscale up --advertise-tags=%s`, or use --mode sidecar",
			strings.Join(missing, ","), settings.Tag)
	}
	if !hostDaemonSupportsServices(self.Version) {
		return fmt.Errorf("the host tailscaled reports version %q, but Tailscale Services needs v%d.%d or newer; upgrade Tailscale on this host, or use --mode sidecar",
			self.Version, minHostDaemonMajor, minHostDaemonMinor)
	}
	if controlSeamActive() {
		return nil
	}
	return validateHostInTailnet(ctx, api, self.NodeKey)
}

// mintSidecarAuthKey mints the tagged auth key the once-tailscale sidecar
// registers with. It shares createTailscaleAuthKey with the enable-time probe,
// so the key the sidecar boots with always has exactly the shape the probe
// proved could be minted.
func mintSidecarAuthKey(ctx context.Context, api tailscaleAPI, settings TailscaleSettings) (string, error) {
	_, key, err := createTailscaleAuthKey(ctx, api, settings.Tag)
	return key, err
}

// requireServeEnabled fails the verify before its 90s advertise wait when the
// tailnet has never had Serve enabled. In that state `tailscale serve` prints
// an enablement link and then blocks until someone clicks it, so the serve
// Docktail execs never returns and no service is ever advertised. The link
// embeds the node ID and so cannot be written down anywhere: probing for it is
// the only way to put it in front of the user. A probe that cannot run says
// nothing either way, so it lets the wait proceed rather than inventing a
// diagnosis.
func requireServeEnabled(ctx context.Context, reader TailnetReader) error {
	url, err := reader.ServeEnablementURL(ctx)
	if err != nil || url == "" {
		return nil
	}
	return fmt.Errorf("Tailscale Serve is not enabled on this tailnet, so Once's Services can never be advertised; enable it by opening %s, then re-run `once tailscale enable`", url)
}

// waitForAdvertised polls the local daemon's serve status until every expected
// service is advertised, or fails at timeout: with the daemon's error if it
// couldn't be read, else naming the services still missing.
func waitForAdvertised(ctx context.Context, reader TailnetReader, expected []string, hint string, timeout, poll time.Duration, announce func(string)) error {
	if announce != nil {
		announce(fmt.Sprintf("waiting up to %s for the local tailscaled to advertise: %s", timeout, strings.Join(expected, ", ")))
	}
	deadline := time.Now().Add(timeout)
	missing := expected
	for {
		status, err := reader.ServeStatus(ctx)
		if err == nil {
			missing = missingServices(status.Services, expected)
			if len(missing) == 0 {
				return nil
			}
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("reading serve status from the local tailscaled: %w", err)
			}
			return fmt.Errorf("not yet advertised by the local tailscaled: %s; check %s", strings.Join(missing, ", "), hint)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

// waitForApproved polls the cloud API's service-hosts endpoint (the same one
// Docktail uses) until every expected service has an approved advertising
// host. A host stuck at not-approved fails immediately: approval needs a
// human or an autoApprovers rule, so waiting cannot fix it.
func waitForApproved(ctx context.Context, api tailscaleAPI, settings TailscaleSettings, expected []string, hint string, timeout, poll time.Duration, announce func(string)) error {
	if announce != nil {
		announce(fmt.Sprintf("waiting up to %s for the tailnet to approve: %s", timeout, strings.Join(expected, ", ")))
	}

	deadline := time.Now().Add(timeout)
	unapproved := expected
	for {
		var waiting []string
		for _, name := range unapproved {
			state, err := serviceApprovalState(ctx, api, name)
			if err != nil {
				return err
			}
			switch state {
			case serviceApproved:
			case servicePending:
				return fmt.Errorf("service %q is advertised but pending approval in the tailnet; approve it in the Tailscale admin console (Services), or add an autoApprovers.services rule for %s so services are approved automatically", name, settings.Tag)
			default:
				waiting = append(waiting, name)
			}
		}
		unapproved = waiting
		if len(unapproved) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("never appeared in the tailnet: %s; check %s", strings.Join(unapproved, ", "), hint)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

type approvalState int

const (
	serviceUnknown approvalState = iota // not (yet) known to the control plane
	servicePending                      // advertised, but no approved host
	serviceApproved
)

// serviceApprovalState reads which devices advertise svc:<name> and whether
// any is approved, via GET /tailnet/-/services/svc:<name>/devices - the same
// request Docktail's listServiceHosts sends. approvalLevel is one of
// not-approved, approved:auto, approved:manual.
func serviceApprovalState(ctx context.Context, api tailscaleAPI, name string) (approvalState, error) {
	status, body, err := api.Call(ctx, http.MethodGet, "/tailnet/-/services/svc:"+name+"/devices", nil)
	if err != nil {
		return serviceUnknown, err
	}
	switch {
	case status == http.StatusNotFound:
		// Docktail hasn't created the Service definition yet.
		return serviceUnknown, nil
	case status != http.StatusOK:
		return serviceUnknown, fmt.Errorf("listing devices hosting service %q (HTTP %d): %s", name, status, errorMessage(body))
	}

	var result struct {
		Hosts []struct {
			ApprovalLevel string `json:"approvalLevel"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return serviceUnknown, fmt.Errorf("decoding service %q hosts: %w", name, err)
	}
	if len(result.Hosts) == 0 {
		return serviceUnknown, nil
	}
	for _, host := range result.Hosts {
		if strings.HasPrefix(host.ApprovalLevel, "approved") {
			return serviceApproved, nil
		}
	}
	return servicePending, nil
}

// missingServices returns the expected service names absent from advertised.
func missingServices(advertised, expected []string) []string {
	var missing []string
	for _, name := range expected {
		if !slices.Contains(advertised, name) {
			missing = append(missing, name)
		}
	}
	return missing
}

// ServiceStatus is one row of `once tailscale status`: a service's tailnet
// URL and its advertised/approved state rendered for display. FunnelURL and
// FunnelExpiresAt are set only while the service has an active Funnel, giving
// its public address and when that access closes.
type ServiceStatus struct {
	Name            string     `json:"name"`
	URL             string     `json:"url"`
	State           string     `json:"state"`
	FunnelURL       string     `json:"funnelURL,omitempty"`
	FunnelExpiresAt *time.Time `json:"funnelExpiresAt,omitempty"`
}

func serviceStatuses(ctx context.Context, reader TailnetReader, api tailscaleAPI, settings TailscaleSettings, names []string, hint string) ([]ServiceStatus, error) {
	serve, err := reader.ServeStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading serve status from the local tailscaled: %w", err)
	}

	cloudChecked := !controlSeamActive()
	if cloudChecked {
		if err := checkOAuthCredentials(ctx, api); err != nil {
			return nil, err
		}
	}

	statuses := make([]ServiceStatus, 0, len(names))
	for _, name := range names {
		advertised := slices.Contains(serve.Services, name)
		approval := serviceUnknown
		if cloudChecked && advertised {
			if approval, err = serviceApprovalState(ctx, api, name); err != nil {
				return nil, err
			}
		}
		statuses = append(statuses, ServiceStatus{
			Name:  name,
			URL:   settings.TailnetURL(name),
			State: renderServiceState(advertised, approval, cloudChecked, settings.Tag, hint),
		})
	}
	return statuses, nil
}

// checkOAuthCredentials proves the OAuth credentials before any service state
// is reported, via the seam's token-only Authenticate (ADR-0011) - so the
// check exchanges the token alone and never touches tailnet data it does not
// need, and cannot fail for tailnet-shaped reasons. Without a call of its own,
// a revoked credential would only be noticed wherever the per-service cloud
// check happens to run - never, when nothing is advertised, and mid-listing
// when only some services are. Everything but a token endpoint that is merely
// rate-limited or broken is fatal here, forwarded unchanged: that one case
// never judged the credentials, so it stays the per-service checks' business
// to report, as it was before this check existed.
func checkOAuthCredentials(ctx context.Context, api tailscaleAPI) error {
	err := api.Authenticate(ctx)
	if err == nil || errors.Is(err, ErrOAuthTokenEndpointUnavailable) {
		return nil
	}
	return err
}

// renderServiceState turns the two verification sources into one display
// state. Advertisement is the local daemon's fact, approval the cloud's: an
// unadvertised service is unreachable no matter what the tailnet thinks, and
// a pending one needs a human (or an autoApprovers rule), so each gets its
// own actionable message.
func renderServiceState(advertised bool, approval approvalState, cloudChecked bool, tag, hint string) string {
	switch {
	case !advertised:
		return fmt.Sprintf("not advertised (check %s)", hint)
	case !cloudChecked:
		return "advertised"
	case approval == serviceApproved:
		return "approved"
	case approval == servicePending:
		return fmt.Sprintf("pending approval (approve it in the Tailscale admin console, or add an autoApprovers.services rule for %s)", tag)
	default:
		return fmt.Sprintf("advertised, but not yet in the tailnet (check %s)", hint)
	}
}

// pullImageRef pulls an image and drains its progress stream.
func pullImageRef(ctx context.Context, c imagePuller, ref string) error {
	reader, err := c.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pulling %s: %w", ref, err)
	}
	defer reader.Close()
	_, _ = io.Copy(io.Discard, reader)
	return nil
}

// forceRemoveContainer removes a container by name, tolerating one that is
// already gone.
func forceRemoveContainer(ctx context.Context, c containerRemover, name string) error {
	if err := c.ContainerRemove(ctx, name, container.RemoveOptions{Force: true}); err != nil {
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("removing %s: %w", name, err)
		}
	}
	return nil
}

// hostTailscaledDir is the host directory tailscaled publishes its socket in,
// bind-mounted into docktail in host mode. Overridable so tests can fake a
// host daemon with a temp dir.
func hostTailscaledDir() string {
	if d := os.Getenv("ONCE_TAILSCALED_DIR"); d != "" {
		return d
	}
	return tailscaledSocketDir
}

// hostTailscaledPresent detects a tailscaled already running on the host by
// its socket. It no longer decides the mode (the sidecar is the default); it
// only triggers the fresh-enable notice pointing at --mode host.
func hostTailscaledPresent() bool {
	info, err := os.Stat(filepath.Join(hostTailscaledDir(), tailscaledSocketFile))
	return err == nil && !info.IsDir()
}

// hostModeNotice returns the one-line notice that keeps host mode discoverable
// (ADR-0007): a fresh enable that took the sidecar default while a host
// tailscaled is running. An explicit mode or a re-enable never earns it.
func hostModeNotice(requested string, fresh bool) string {
	if requested != "" || !fresh || !hostTailscaledPresent() {
		return ""
	}
	return "a tailscaled is already running on this host; Once boots its own sidecar by default - re-run with `--mode host` to drive the host daemon instead"
}

// sidecarWedged reports whether a sidecar container state means it cannot
// come up on its own: restart-looping, or exited with a failure. A manually
// stopped sidecar usually exits nonzero (SIGTERM) and so also reads as wedged;
// the rebuild that follows is harmless since the state volume is kept.
func sidecarWedged(state *container.State) bool {
	if state == nil {
		return false
	}
	return state.Restarting || (state.Status == "exited" && state.ExitCode != 0)
}

// waitForSidecarLogin polls until the sidecar's tailscaled reports a Running
// backend (registered and logged in). A crashed container fails immediately
// via the crashed probe. Timing out is non-fatal, like the suffix wait: a slow
// control plane must not fail enable, and the later waits surface persistent
// problems. reader, crashed, and the durations are parameters so tests can
// drive the wait with fakes.
func waitForSidecarLogin(ctx context.Context, reader TailnetReader, crashed func(context.Context) (string, bool), timeout, poll time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if msg, ok := crashed(ctx); ok {
			return errors.New(msg)
		}
		if self, err := reader.SelfStatus(ctx); err == nil && self.BackendState == "Running" {
			return nil
		}
		if time.Now().After(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

// waitForMagicDNSSuffix polls the local daemon until it reports the tailnet's
// Magic DNS suffix. Bounded and non-fatal: returns "" on timeout, so a slow
// control-plane join never fails enable - DomainSuffix() lazily persists the
// suffix once the daemon reports it.
func waitForMagicDNSSuffix(ctx context.Context, reader TailnetReader) (string, error) {
	deadline := time.Now().Add(suffixWaitTimeout)
	for {
		suffix, err := reader.MagicDNSSuffix(ctx)
		if err == nil && suffix != "" {
			return suffix, nil
		}
		if time.Now().After(deadline) {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(suffixWaitPoll):
		}
	}
}

// validateHostInTailnet proves the host node is a device in the OAuth
// client's tailnet via the devices-list API, catching a host daemon logged
// into a different tailnet than the credential. The node key is the join key:
// `tailscale status --json` reports it as Self.PublicKey and the devices list
// as nodeKey.
func validateHostInTailnet(ctx context.Context, api tailscaleAPI, nodeKey string) error {
	status, body, err := api.Call(ctx, http.MethodGet, "/tailnet/-/devices", nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("listing the tailnet's devices (HTTP %d): %s", status, errorMessage(body))
	}

	var resp struct {
		Devices []struct {
			NodeKey string `json:"nodeKey"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("decoding the tailnet devices list: %w", err)
	}
	for _, device := range resp.Devices {
		if device.NodeKey == nodeKey {
			return nil
		}
	}
	return fmt.Errorf("the host node is not in the OAuth client's tailnet - the host tailscaled is likely logged into a different tailnet; run `sudo tailscale login` with the account that owns the OAuth client, or use --mode sidecar")
}

// missingNodeTags returns the segments of the (normalized, comma-separated)
// required tag that are absent from the node's tags.
func missingNodeTags(required string, present []string) []string {
	var missing []string
	for _, tag := range strings.Split(required, ",") {
		if tag != "" && !slices.Contains(present, tag) {
			missing = append(missing, tag)
		}
	}
	return missing
}

// hostDaemonSupportsServices reports whether a tailscaled version string (e.g.
// "1.88.4-t1234abcd") is at least the Tailscale Services minimum. An
// unparseable version reads as unsupported, so the pre-flight surfaces it.
func hostDaemonSupportsServices(version string) bool {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	minorDigits, _, _ := strings.Cut(parts[1], "-")
	minor, err := strconv.Atoi(minorDigits)
	if err != nil {
		return false
	}
	return major > minHostDaemonMajor || (major == minHostDaemonMajor && minor >= minHostDaemonMinor)
}

// createTailscaleAuthKey mints a tagged auth key of the shape Once uses for
// the once-tailscale sidecar's TS_AUTHKEY: reusable, non-ephemeral (the
// sidecar keeps its node state in a volume), preauthorized, tagged. Reusable
// matters because the sidecar restarts on failure: a single-use key is
// consumed by registration even when the login never finishes (e.g. the
// container is killed waiting for the netmap), which turns one transient
// failure into a permanent "invalid key" crash loop. A 403 means the OAuth
// client lacks the auth_keys scope; any other rejection means it does not own
// the requested tag.
func createTailscaleAuthKey(ctx context.Context, api tailscaleAPI, tag string) (id, key string, err error) {
	var reqBody tsKeyRequest
	reqBody.Description = "once-tailscale"
	reqBody.Capabilities.Devices.Create.Reusable = true
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
		return "", "", fmt.Errorf("the OAuth client is missing the auth_keys scope (Keys -> Auth Keys: Write), needed to mint the Tailscale sidecar's auth key (HTTP %d): %s", status, errorMessage(body))
	case status == http.StatusBadRequest:
		return "", "", fmt.Errorf("Tailscale refused to mint an auth key for tag %q (HTTP %d): %s; ensure the OAuth client is tagged with this tag, and %w", tag, status, errorMessage(body), ErrTagNotInPolicy)
	case status != http.StatusOK:
		return "", "", fmt.Errorf("Tailscale refused to mint an auth key for tag %q (HTTP %d): %s", tag, status, errorMessage(body))
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

func deleteProbeKey(ctx context.Context, api tailscaleAPI, keyID string) error {
	status, _, err := api.Call(ctx, http.MethodDelete, "/tailnet/-/keys/"+url.PathEscape(keyID), nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("deleting probe auth key: HTTP %d", status)
	}
	return nil
}

// probeServiceDefinition proves the services scope (General -> Services: Write)
// and the devices scope (Devices -> Core) with the request shapes Docktail
// sends at runtime: check whether the Service exists (SyncServiceDefinition),
// create it (putService), list its advertising devices (listServiceHosts), and
// delete it (deleteService). The create is also where an undefined tag surfaces.
// The "-" tailnet path segment matches Docktail, which defaults
// TAILSCALE_TAILNET to "-" when Once leaves it unset.
func probeServiceDefinition(ctx context.Context, api tailscaleAPI, tag string) error {
	svcPath := "/tailnet/-/services/" + probeServiceName

	status, body, err := api.Call(ctx, http.MethodGet, svcPath, nil)
	if err != nil {
		return err
	}
	switch status {
	case http.StatusNotFound:
		// The name is free, as expected.
	case http.StatusOK:
		var existing struct {
			Comment string `json:"comment"`
		}
		if json.Unmarshal(body, &existing) != nil || existing.Comment != probeServiceComment {
			return fmt.Errorf("a Tailscale Service named %s already exists in the tailnet; Once needs that name free for its credential probe, so remove or rename that Service and re-run", probeServiceName)
		}
		// A leftover from an interrupted probe: safe to overwrite and delete.
	case http.StatusForbidden:
		return servicesScopeError(status, body)
	default:
		return fmt.Errorf("checking for an existing %s Service (HTTP %d): %s", probeServiceName, status, errorMessage(body))
	}

	// Byte-compatible with Docktail's putService create payload.
	payload, err := json.Marshal(map[string]any{
		"name":    probeServiceName,
		"tags":    strings.Split(tag, ","),
		"ports":   []string{"tcp:443"},
		"comment": probeServiceComment,
	})
	if err != nil {
		return err
	}
	status, body, err = api.Call(ctx, http.MethodPut, svcPath, payload)
	if err != nil {
		return err
	}
	switch {
	case status == http.StatusForbidden:
		return servicesScopeError(status, body)
	case status == http.StatusBadRequest:
		return fmt.Errorf("Tailscale rejected the probe Service definition for tag %q (HTTP %d): %s; %w", tag, status, errorMessage(body), ErrTagNotInPolicy)
	case status != http.StatusOK:
		return fmt.Errorf("Tailscale rejected the probe Service definition for tag %q (HTTP %d): %s", tag, status, errorMessage(body))
	}

	// The Service now exists; clean it up whether or not the devices check passes.
	devErr := probeServiceDevices(ctx, api, svcPath)
	_ = deleteProbeService(ctx, api, svcPath)
	return devErr
}

func probeServiceDevices(ctx context.Context, api tailscaleAPI, svcPath string) error {
	status, body, err := api.Call(ctx, http.MethodGet, svcPath+"/devices", nil)
	if err != nil {
		return err
	}
	switch {
	case status == http.StatusForbidden:
		return fmt.Errorf("the OAuth client is missing the devices scope (Devices -> Core: Write), needed to check which hosts advertise a Service (HTTP %d): %s", status, errorMessage(body))
	case status != http.StatusOK:
		return fmt.Errorf("listing the probe Service's devices (HTTP %d): %s", status, errorMessage(body))
	}
	return nil
}

func deleteProbeService(ctx context.Context, api tailscaleAPI, svcPath string) error {
	status, body, err := api.Call(ctx, http.MethodDelete, svcPath, nil)
	if err != nil {
		return err
	}
	// Docktail's deleteService treats 404 as success; so does the probe.
	if status != http.StatusOK && status != http.StatusNotFound {
		return fmt.Errorf("deleting the probe Service (HTTP %d): %s", status, errorMessage(body))
	}
	return nil
}

func servicesScopeError(status int, body []byte) error {
	return fmt.Errorf("the OAuth client is missing the services scope (General -> Services: Write), needed to manage Tailscale Service definitions (HTTP %d): %s", status, errorMessage(body))
}

func errorMessage(body []byte) string {
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &e) == nil && e.Message != "" {
		return e.Message
	}
	return strings.TrimSpace(string(body))
}
