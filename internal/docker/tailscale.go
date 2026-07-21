package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
)

// Images are pinned to exact versions, the same doctrine as proxyImage.
// Bumped via Once releases through the self-update mechanism. The tailscale
// image must be v1.86.0+ for Tailscale Services.
const (
	docktailImage         = "ghcr.io/marvinvr/docktail:1.5.0"
	tailscaleSidecarImage = "tailscale/tailscale:v1.88.4"
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

// TailscaleSettings.Mode values: where the tailscaled that backs Docktail
// lives, so status and disable know what Once manages. The sidecar is the
// default; host mode is explicit opt-in (ADR-0007).
const (
	TailscaleModeHost    = "host"
	TailscaleModeSidecar = "sidecar"
)

// ValidateTailscaleMode rejects a requested mode other than host, sidecar, or
// empty (empty keeps an already-enabled install's stored mode and defaults a
// fresh enable to the sidecar).
func ValidateTailscaleMode(mode string) error {
	switch mode {
	case "", TailscaleModeHost, TailscaleModeSidecar:
		return nil
	}
	return fmt.Errorf("invalid Tailscale mode %q: must be %q or %q", mode, TailscaleModeHost, TailscaleModeSidecar)
}

// Funnel durations: a short default so apps don't stay public by accident, and
// a hard 24h cap (see PRD "Out of Scope").
const (
	DefaultFunnelDuration = 10 * time.Minute
	MaxFunnelDuration     = 24 * time.Hour
)

// funnelActivation bounds how long we wait for the local tailscaled to report
// a Funnel active before surfacing a failure. ponytail: fixed poll, fine for an
// interactive enable; lengthen if first-time activation proves slower.
const (
	funnelActivationTimeout = 15 * time.Second
	funnelActivationPoll    = time.Second
)

// suffixWait bounds the post-boot wait for tailscaled to join the control
// plane and report the Magic DNS suffix. Non-fatal on timeout: DomainSuffix()
// lazily reads and persists it later, so a slow tailnet never fails enable.
const (
	suffixWaitTimeout = 20 * time.Second
	suffixWaitPoll    = 2 * time.Second
)

var (
	ErrFunnelDurationInvalid = errors.New("funnel duration must be positive")
	ErrFunnelDurationTooLong = fmt.Errorf("funnel duration must not exceed %s", MaxFunnelDuration)
)

// ValidateFunnelDuration enforces the positive/≤24h bounds on a requested
// Funnel duration.
func ValidateFunnelDuration(d time.Duration) error {
	if d <= 0 {
		return ErrFunnelDurationInvalid
	}
	if d > MaxFunnelDuration {
		return ErrFunnelDurationTooLong
	}
	return nil
}

// Hidden control-server seam: when these env vars are set, the once-tailscale
// sidecar joins controlUrl with the pre-minted authKey instead of Once minting
// one via OAuth, and docktail runs without cloud credentials (manual mode). No
// CLI/TUI exposes them - they exist to point the sidecar at the headscale test
// harness, and are promotable to first-class headscale support later.
const (
	envControlURL = "ONCE_TAILSCALE_CONTROL_URL"
	envAuthKey    = "ONCE_TAILSCALE_AUTH_KEY"
)

type TailscaleSettings struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	// Tag is the Tailscale tag (e.g. tag:once) the sidecar advertises and
	// docktail stamps on the Service definitions it creates. A tagged OAuth
	// client can only mint keys for tags it owns, so this must be a tag the
	// client owns in the tailnet ACL. Empty on the headscale control seam, which
	// uses a pre-minted auth key instead.
	Tag string `json:"tag,omitempty"`
	// DomainSuffix is the tailnet's MagicDNS base (e.g. tailnet-name.ts.net),
	// read from the local tailscaled at enable time. With it stored, app tailnet
	// URLs are computed without any live lookup.
	DomainSuffix string `json:"domainSuffix,omitempty"`
	// Mode records whether Docktail drives a tailscaled already on the host
	// (TailscaleModeHost) or the Once-managed once-tailscale sidecar
	// (TailscaleModeSidecar).
	Mode string `json:"mode,omitempty"`
}

// TailnetURL builds an app's https://<app>.<suffix> tailnet URL from the stored
// domain suffix, or "" when the suffix isn't known yet.
func (s TailscaleSettings) TailnetURL(appName string) string {
	if s.DomainSuffix == "" {
		return ""
	}
	return "https://" + appName + "." + s.DomainSuffix
}

func UnmarshalTailscaleSettings(s string) (TailscaleSettings, error) {
	var settings TailscaleSettings
	err := json.Unmarshal([]byte(s), &settings)
	return settings, err
}

func (s TailscaleSettings) Marshal() string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TailscaleControlSeamActive reports whether the hidden headscale control seam
// is configured, so callers can skip Tailscale SaaS-only flows (like the
// policy API) that do not exist on headscale.
func TailscaleControlSeamActive() bool {
	return controlSeamActive()
}

// Private

// normalizeTag prepends "tag:" to each comma-separated segment that lacks it, so
// a bare name like "once" becomes "tag:once" before it reaches the Tailscale API
// or the docktail config. Already-prefixed segments pass through unchanged. A tag
// that is empty (or only whitespace) collapses to "" so the downstream "a tag is
// required" check still fires rather than a blank tag reaching the API.
func (s TailscaleSettings) normalizeTag() string {
	if strings.TrimSpace(s.Tag) == "" {
		return ""
	}
	segments := strings.Split(s.Tag, ",")
	for i, seg := range segments {
		seg = strings.TrimSpace(seg)
		if !strings.HasPrefix(seg, "tag:") {
			seg = "tag:" + seg
		}
		segments[i] = seg
	}
	return strings.Join(segments, ",")
}

// Tailscale manages the Docktail topology: the once-docktail container that
// advertises labelled containers as Tailscale Services, plus - in the default
// sidecar mode - the unprivileged once-tailscale sidecar it drives. Mirroring
// Proxy, the once-docktail container's existence is the
// source of truth for "Tailscale enabled"; its settings live in the once label
// on the container itself.
type Tailscale struct {
	namespace    *Namespace
	Settings     *TailscaleSettings
	domainSuffix string // in-memory cache so a learned suffix survives a failed persist
}

func NewTailscale(ns *Namespace) *Tailscale {
	return &Tailscale{namespace: ns}
}

// Enable boots the Docktail topology: once-docktail, plus - in sidecar mode -
// the once-tailscale sidecar it drives over a shared socket volume (userspace
// networking, unprivileged). settings.Mode is the requested mode: empty keeps
// an already-enabled install's stored mode and defaults a fresh enable to the
// sidecar; host mode is explicit opt-in (ADR-0007). Re-running with the same
// credentials and mode just ensures everything is running; changed credentials
// or mode rebuild the topology while the sidecar's state volume (node
// identity) is kept. The returned notice, when non-empty, tells the user a
// fresh enable defaulted to the sidecar even though a host tailscaled is
// present.
func (t *Tailscale) Enable(ctx context.Context, settings TailscaleSettings) (string, error) {
	var notice string
	info, err := t.namespace.client.ContainerInspect(ctx, t.containerName())
	switch {
	case err == nil:
		existing, _ := UnmarshalTailscaleSettings(info.Config.Labels[labelKey])
		if settings.Mode == "" {
			settings.Mode = existing.Mode
		}
		if existing.ClientID == settings.ClientID && existing.ClientSecret == settings.ClientSecret && existing.Tag == settings.Tag && existing.Mode == settings.Mode {
			return "", t.ensureRunning(ctx, info)
		}
		// The sidecar's auth key and advertised tag derive from the credentials,
		// the suffix may belong to a different tailnet now, and a mode change
		// moves where tailscaled lives - either way, rebuild the topology from
		// scratch (keeping the state volume).
		if err := t.removeTopology(ctx); err != nil {
			return "", err
		}
	case !errdefs.IsNotFound(err):
		return "", fmt.Errorf("inspecting docktail container: %w", err)
	default:
		if settings.Mode == "" && hostTailscaledPresent() {
			notice = "a tailscaled is already running on this host; Once boots its own sidecar by default - re-run with `--mode host` to drive the host daemon instead"
		}
	}
	// Also covers a rebuild whose stored settings carry no mode (corrupt or
	// legacy label): defaulting here self-heals it to the sidecar.
	if settings.Mode == "" {
		settings.Mode = TailscaleModeSidecar
	}

	if settings.Mode == TailscaleModeSidecar {
		if err := t.bootSidecar(ctx, settings); err != nil {
			return "", err
		}
	}

	// Bake the suffix into the settings label now so the container is created
	// once, not created and immediately recreated by a persist.
	if settings.DomainSuffix == "" {
		settings.DomainSuffix, _ = waitForMagicDNSSuffix(ctx, t.daemonFor(settings.Mode))
	}

	if err := t.pullImage(ctx, docktailImage); err != nil {
		return "", err
	}
	if err := t.create(ctx, settings); err != nil {
		return "", err
	}
	t.Settings = &settings

	// Retrofit running apps so they appear on the tailnet immediately, not just
	// on their next deploy. once-docktail now exists, so the roll injects labels.
	return notice, t.namespace.RollApplications(ctx)
}

// Disable removes once-docktail and the Once-managed sidecar, keeping the
// sidecar's state volume so the node identity survives a later re-enable
// without Magic DNS churn.
func (t *Tailscale) Disable(ctx context.Context) error {
	if err := t.removeTopology(ctx); err != nil {
		return err
	}
	t.Settings = nil

	// Roll running apps to strip the docktail.* labels - once-docktail is gone,
	// so Enabled() is now false and the roll recreates them without the labels.
	return t.namespace.RollApplications(ctx)
}

// Enabled is implicit in the existence of the once-docktail container (the same
// doctrine the proxy follows).
func (t *Tailscale) Enabled(ctx context.Context) (bool, error) {
	_, err := t.namespace.client.ContainerInspect(ctx, t.containerName())
	if err == nil {
		return true, nil
	}
	if errdefs.IsNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("inspecting docktail container: %w", err)
}

// LoadSettings reads the Tailscale settings stored in the once-docktail
// container label, for pre-populating UIs. Errors if Tailscale isn't enabled.
func (t *Tailscale) LoadSettings(ctx context.Context) (TailscaleSettings, error) {
	info, err := t.namespace.client.ContainerInspect(ctx, t.containerName())
	if err != nil {
		return TailscaleSettings{}, fmt.Errorf("inspecting docktail container: %w", err)
	}
	return UnmarshalTailscaleSettings(info.Config.Labels[labelKey])
}

// DomainSuffix returns the tailnet's MagicDNS suffix (e.g. tailnet-name.ts.net).
// It prefers the value persisted in settings; otherwise it reads the suffix
// from the local tailscaled and persists it, so enables that raced a slow
// control plane self-heal. Returns "" when the daemon hasn't joined the
// tailnet yet, so the suffix can't be known.
func (t *Tailscale) DomainSuffix(ctx context.Context) (string, error) {
	if t.domainSuffix != "" {
		return t.domainSuffix, nil
	}

	settings, err := t.LoadSettings(ctx)
	if err != nil {
		return "", err
	}
	if settings.DomainSuffix != "" {
		t.domainSuffix = settings.DomainSuffix
		return settings.DomainSuffix, nil
	}

	suffix, err := t.daemonFor(settings.Mode).MagicDNSSuffix(ctx)
	if err != nil || suffix == "" {
		return "", err
	}
	// ponytail: Docker labels are immutable, so baking the suffix into the
	// settings label means one docktail recreate - cheap, docktail is stateless.
	// Cache the suffix first so a failed persist still short-circuits here next
	// time rather than recreating on every poll.
	t.domainSuffix = suffix
	settings.DomainSuffix = suffix
	_ = t.persistSettings(ctx, settings)
	return suffix, nil
}

// WaitForFunnelActive polls the local tailscaled until it reports a Funnel
// proxying to the app's container, or returns an error on timeout. Funnel is
// per-node, so matching the app's backend is what proves *this* app went
// public - another app's active Funnel must not count. Funnel also needs the
// tailnet ACL's funnel node attribute, which Once can't manage - surfacing
// activation failure prevents reporting a Funnel active when it isn't.
func (t *Tailscale) WaitForFunnelActive(ctx context.Context, app *Application) error {
	settings, err := t.LoadSettings(ctx)
	if err != nil {
		return err
	}
	backend, err := app.funnelBackend(ctx)
	if err != nil {
		return err
	}
	return waitForFunnelBackend(ctx, t.daemonFor(settings.Mode), backend, funnelActivationTimeout, funnelActivationPoll)
}

// LegacyTSDProxyWarning returns a message telling the user how to move off a
// pre-Docktail TSDProxy install when its container is still around, or "" when
// there is none. Once never manages that container: the user re-runs enable to
// get the Docktail topology and removes the old container by hand (only the
// full teardown in Destroy sweeps it up). Best effort - any inspect error
// reads as "not present".
func (t *Tailscale) LegacyTSDProxyWarning(ctx context.Context) string {
	name := t.legacyTSDProxyName()
	if _, err := t.namespace.client.ContainerInspect(ctx, name); err != nil {
		return ""
	}
	return fmt.Sprintf("found a %s container from an older version of Once, which Once no longer manages; re-run `once tailscale enable` to set up the new Tailscale integration, then remove it with `docker rm -f %s` and `docker volume rm %s`",
		name, name, t.legacyTSDProxyVolumeName())
}

// Destroy removes the full topology including the sidecar's state volume (full
// cleanup, used by teardown - unlike Disable, which retains it). It also sweeps
// up the legacy once-tsdproxy container and volume from installs that predate
// the Docktail swap.
func (t *Tailscale) Destroy(ctx context.Context) error {
	if err := t.removeTopology(ctx); err != nil {
		return err
	}
	if err := t.removeVolume(ctx, t.stateVolumeName()); err != nil {
		return err
	}
	if err := t.removeContainer(ctx, t.legacyTSDProxyName()); err != nil {
		return err
	}
	if err := t.removeVolume(ctx, t.legacyTSDProxyVolumeName()); err != nil {
		return err
	}
	t.Settings = nil
	return nil
}

// Private

func (t *Tailscale) containerName() string {
	return t.namespace.name + "-docktail"
}

func (t *Tailscale) sidecarName() string {
	return t.namespace.name + "-tailscale"
}

func (t *Tailscale) stateVolumeName() string {
	return t.namespace.name + "-tailscale-state"
}

func (t *Tailscale) socketVolumeName() string {
	return t.namespace.name + "-tailscale-socket"
}

func (t *Tailscale) legacyTSDProxyName() string {
	return t.namespace.name + "-tsdproxy"
}

func (t *Tailscale) legacyTSDProxyVolumeName() string {
	return t.namespace.name + "-tsdproxy-data"
}

// daemonFor returns the TailscaleDaemon matching where tailscaled runs: the
// host CLI in host mode, docker exec into the sidecar otherwise.
func (t *Tailscale) daemonFor(mode string) TailscaleDaemon {
	if mode == TailscaleModeHost {
		return NewHostTailscaleDaemon()
	}
	return NewSidecarTailscaleDaemon(t.namespace.client, t.sidecarName())
}

// ensureRunning starts the topology's containers if stopped and loads the
// stored settings, making a repeated Enable with unchanged credentials a
// no-op boot (the supporting-container doctrine).
func (t *Tailscale) ensureRunning(ctx context.Context, info container.InspectResponse) error {
	settings, err := UnmarshalTailscaleSettings(info.Config.Labels[labelKey])
	if err != nil {
		return fmt.Errorf("unmarshalling docktail settings: %w", err)
	}
	if settings.Mode == TailscaleModeSidecar {
		if err := t.bootSidecar(ctx, settings); err != nil {
			return err
		}
	}
	if !info.State.Running {
		if err := t.namespace.client.ContainerStart(ctx, info.ID, container.StartOptions{}); err != nil {
			return fmt.Errorf("starting docktail container: %w", err)
		}
	}
	t.Settings = &settings
	return nil
}

// bootSidecar idempotently boots the once-tailscale sidecar: an unprivileged
// tailscale/tailscale container in userspace networking mode - no TUN device,
// no NET_ADMIN, no host networking - with its node state on a volume and its
// socket shared with docktail via another. On the SaaS path its auth key is
// minted with the same shape the enable-time probe proved; on the control seam
// the pre-minted key and control URL are used instead.
func (t *Tailscale) bootSidecar(ctx context.Context, settings TailscaleSettings) error {
	info, err := t.namespace.client.ContainerInspect(ctx, t.sidecarName())
	if err == nil {
		if !info.State.Running {
			if err := t.namespace.client.ContainerStart(ctx, info.ID, container.StartOptions{}); err != nil {
				return fmt.Errorf("starting tailscale sidecar: %w", err)
			}
		}
		return nil
	}
	if !errdefs.IsNotFound(err) {
		return fmt.Errorf("inspecting tailscale sidecar: %w", err)
	}

	authKey, controlURL := os.Getenv(envAuthKey), os.Getenv(envControlURL)
	var extraArgs string
	if authKey != "" && controlURL != "" {
		extraArgs = "--login-server=" + controlURL
	} else {
		if authKey, err = mintSidecarAuthKey(ctx, tailscaleAPIBaseURL, settings); err != nil {
			return err
		}
		extraArgs = "--advertise-tags=" + settings.Tag
	}

	if err := t.pullImage(ctx, tailscaleSidecarImage); err != nil {
		return err
	}

	resp, err := t.namespace.client.ContainerCreate(ctx,
		&container.Config{
			Image: tailscaleSidecarImage,
			Env: []string{
				"TS_AUTHKEY=" + authKey,
				"TS_STATE_DIR=" + sidecarStateDir,
				"TS_SOCKET=" + tailscaledSocketDir + "/" + tailscaledSocketFile,
				"TS_USERSPACE=true",
				"TS_HOSTNAME=" + t.sidecarName(),
				"TS_EXTRA_ARGS=" + extraArgs,
			},
		},
		&container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyAlways},
			LogConfig:     ContainerLogConfig(),
			Mounts: []mount.Mount{
				{Type: mount.TypeVolume, Source: t.stateVolumeName(), Target: sidecarStateDir},
				{Type: mount.TypeVolume, Source: t.socketVolumeName(), Target: tailscaledSocketDir},
			},
		},
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				t.namespace.name: {},
			},
		},
		nil,
		t.sidecarName(),
	)
	if err != nil {
		return fmt.Errorf("creating tailscale sidecar: %w", err)
	}

	if err := t.namespace.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		t.namespace.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return fmt.Errorf("starting tailscale sidecar: %w", err)
	}
	return nil
}

// create boots once-docktail: Docker socket read-only, the tailscaled socket
// directory mounted from the host or the sidecar's socket volume. With OAuth
// credentials docktail also syncs Service definitions to the control plane; on
// the headscale control seam it runs in manual mode (no cloud API).
func (t *Tailscale) create(ctx context.Context, settings TailscaleSettings) error {
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
			{Type: mount.TypeVolume, Source: t.socketVolumeName(), Target: tailscaledSocketDir},
		}
	}

	resp, err := t.namespace.client.ContainerCreate(ctx,
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
				t.namespace.name: {},
			},
		},
		nil,
		t.containerName(),
	)
	if err != nil {
		return fmt.Errorf("creating docktail container: %w", err)
	}

	if err := t.namespace.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		t.namespace.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return fmt.Errorf("starting docktail container: %w", err)
	}
	return nil
}

// persistSettings rewrites the once-docktail container's settings label by
// recreating it (Docker labels are immutable on a live container). The sidecar
// is untouched. Used to bake in a newly-learned domain suffix.
func (t *Tailscale) persistSettings(ctx context.Context, settings TailscaleSettings) error {
	if err := t.removeContainer(ctx, t.containerName()); err != nil {
		return err
	}
	if err := t.create(ctx, settings); err != nil {
		return err
	}
	t.Settings = &settings
	return nil
}

// removeTopology removes once-docktail, the Once-managed sidecar, and the
// transient socket volume, keeping the sidecar's state volume.
func (t *Tailscale) removeTopology(ctx context.Context) error {
	if err := t.removeContainer(ctx, t.containerName()); err != nil {
		return err
	}
	if err := t.removeContainer(ctx, t.sidecarName()); err != nil {
		return err
	}
	return t.removeVolume(ctx, t.socketVolumeName())
}

func (t *Tailscale) pullImage(ctx context.Context, ref string) error {
	reader, err := t.namespace.client.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pulling %s: %w", ref, err)
	}
	defer reader.Close()
	_, _ = io.Copy(io.Discard, reader)
	return nil
}

func (t *Tailscale) removeContainer(ctx context.Context, name string) error {
	if err := t.namespace.client.ContainerRemove(ctx, name, container.RemoveOptions{Force: true}); err != nil {
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("removing %s: %w", name, err)
		}
	}
	return nil
}

func (t *Tailscale) removeVolume(ctx context.Context, name string) error {
	if err := t.namespace.client.VolumeRemove(ctx, name, true); err != nil {
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("removing volume %s: %w", name, err)
		}
	}
	return nil
}

// Helpers

// docktailLabels returns the labels that expose an app as a Tailscale Service
// under its own name. Nil when Tailscale is disabled, so app containers carry
// the labels only while once-docktail is running. The port-80 backend matches
// Once's port-80 assumption; the https service protocol makes the tailnet side
// serve on 443, matching the https:// URLs Once displays; network pins direct
// container-IP proxying to the namespace network the app is attached to.
//
// When funnel is set, the same backend is also exposed to the public internet
// via docktail.funnel.* (public side 443/https).
func docktailLabels(appName, network string, enabled, funnel bool) map[string]string {
	if !enabled {
		return nil
	}
	labels := map[string]string{
		"docktail.service.enable":           "true",
		"docktail.service.name":             appName,
		"docktail.service.port":             "80",
		"docktail.service.service-protocol": "https",
		"docktail.service.network":          network,
	}
	if funnel {
		labels["docktail.funnel.enable"] = "true"
		labels["docktail.funnel.port"] = "80"
	}
	return labels
}

// controlSeamActive reports whether the hidden headscale control seam is
// configured, replacing OAuth credential minting and the Tailscale SaaS API.
func controlSeamActive() bool {
	return os.Getenv(envControlURL) != "" && os.Getenv(envAuthKey) != ""
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

// waitForFunnelBackend polls the local daemon until a funnel-enabled endpoint
// proxies to backend. Timeout and poll are parameters so tests can run the
// timeout path fast.
func waitForFunnelBackend(ctx context.Context, daemon TailscaleDaemon, backend string, timeout, poll time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		status, err := daemon.ServeStatus(ctx)
		if err == nil && slices.Contains(status.FunnelBackends, backend) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("funnel did not activate; check that the once-docktail container is running and your tailnet ACL grants the funnel node attribute")
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
func waitForMagicDNSSuffix(ctx context.Context, daemon TailscaleDaemon) (string, error) {
	deadline := time.Now().Add(suffixWaitTimeout)
	for {
		suffix, err := daemon.MagicDNSSuffix(ctx)
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
