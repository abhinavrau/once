package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

var (
	ErrFunnelDurationInvalid = errors.New("funnel duration must be positive")
	ErrFunnelDurationTooLong = fmt.Errorf("funnel duration must not exceed %s", MaxFunnelDuration)
	ErrFunnelPortsExhausted  = errors.New("all Funnel public ports (443, 8443, 10000) are in use; disable another app's Funnel first")
)

// funnelPublicPorts are the public ports Tailscale Funnel can serve HTTPS on.
var funnelPublicPorts = []int{443, 8443, 10000}

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
	// NodeHost is the sidecar node's own tailnet hostname (e.g.
	// once-tailscale-1.tailnet-name.ts.net), learned the first time a Funnel
	// activates. Funnel is per-node, so every app's Funnel URL is this host
	// with the app's public port - stored so those URLs need no live lookup.
	NodeHost string `json:"nodeHost,omitempty"`
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

// FunnelURL builds the public URL a Funnel on the given public port serves,
// from the stored node hostname, or "" when the node host isn't known yet.
// Funnel is per-node: all apps share this host and differ only by port.
func (s TailscaleSettings) FunnelURL(port int) string {
	if s.NodeHost == "" || port == 0 {
		return ""
	}
	return funnelURL(s.NodeHost + ":" + strconv.Itoa(port))
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
	// awaitLogin is a hook for the post-boot sidecar login wait, which runs the
	// tailscale CLI over a hijacked docker exec connection and so is unfakeable
	// at the boot dispatch's client seam (ADR-0009). Nil means the real wait;
	// only tests override it.
	awaitLogin func(context.Context) error
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
// proxying to the app's container, then returns the funnel's public URL - the
// node's own hostname, not the app's tailnet service name, because Funnel is
// per-node. That per-node scoping is also why matching the app's backend is
// what proves *this* app went public - another app's active Funnel must not
// count. Funnel also needs the tailnet ACL's funnel node attribute, which
// Once can't manage - surfacing activation failure prevents reporting a
// Funnel active when it isn't.
func (t *Tailscale) WaitForFunnelActive(ctx context.Context, app *Application) (string, error) {
	settings, err := t.LoadSettings(ctx)
	if err != nil {
		return "", err
	}
	backend, err := app.funnelBackend(ctx)
	if err != nil {
		return "", err
	}
	hostPort, err := waitForFunnelBackend(ctx, t.daemonFor(settings.Mode), backend, funnelActivationTimeout, funnelActivationPoll)
	if err != nil {
		return "", err
	}
	// Persist the node's hostname (write-through, like DomainSuffix) so status
	// and the TUI can render every Funnel URL without a live serve read.
	if host, _, ok := strings.Cut(hostPort, ":"); ok && host != settings.NodeHost {
		settings.NodeHost = host
		_ = t.persistSettings(ctx, settings)
	}
	return funnelURL(hostPort), nil
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

// effectiveMode resolves the mode an Enable with the requested mode would run
// in, mirroring Enable's own resolution: an explicit mode wins, an empty one
// keeps an already-enabled install's stored mode (tolerating a corrupt label,
// which Enable self-heals to the sidecar), and a fresh enable defaults to the
// sidecar. A real inspect failure is an error, not a silent sidecar default:
// callers use this to decide whether the host-mode pre-flight must run, and
// guessing wrong would skip it.
func (t *Tailscale) effectiveMode(ctx context.Context, requested string) (string, error) {
	if requested != "" {
		return requested, nil
	}
	info, err := t.namespace.client.ContainerInspect(ctx, t.containerName())
	if errdefs.IsNotFound(err) {
		return TailscaleModeSidecar, nil
	}
	if err != nil {
		return "", fmt.Errorf("inspecting docktail container: %w", err)
	}
	stored, _ := UnmarshalTailscaleSettings(info.Config.Labels[labelKey])
	if stored.Mode != "" {
		return stored.Mode, nil
	}
	return TailscaleModeSidecar, nil
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
func (t *Tailscale) bootSidecar(ctx context.Context, settings TailscaleSettings) error {
	return t.bootSidecarWithClient(ctx, t.namespace.client, settings)
}

// bootSidecarWithClient is bootSidecar's dispatch, reaching the engine by
// parameter rather than through the namespace, so tests drive every branch -
// crash-looping, stopped, running, missing - with a fake.
func (t *Tailscale) bootSidecarWithClient(ctx context.Context, c sidecarBootClient, settings TailscaleSettings) error {
	info, err := c.ContainerInspect(ctx, t.sidecarName())
	switch {
	case err == nil && sidecarWedged(info.State):
		// A crash-looping sidecar can't be started out of its loop: its baked-in
		// TS_AUTHKEY may be dead (revoked, expired). Rebuild it with a freshly
		// minted key - the state volume survives, so the node identity does too.
		if err := forceRemoveContainer(ctx, c, t.sidecarName()); err != nil {
			return err
		}
	case err == nil:
		if !info.State.Running {
			if err := c.ContainerStart(ctx, info.ID, container.StartOptions{}); err != nil {
				return fmt.Errorf("starting tailscale sidecar: %w", err)
			}
		}
		return t.awaitSidecarLogin(ctx)
	case !errdefs.IsNotFound(err):
		return fmt.Errorf("inspecting tailscale sidecar: %w", err)
	}

	authKey, controlURL := os.Getenv(envAuthKey), os.Getenv(envControlURL)
	var extraArgs string
	if authKey != "" && controlURL != "" {
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

	if err := c.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		c.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return fmt.Errorf("starting tailscale sidecar: %w", err)
	}
	return t.awaitSidecarLogin(ctx)
}

func (t *Tailscale) awaitSidecarLogin(ctx context.Context) error {
	if t.awaitLogin != nil {
		return t.awaitLogin(ctx)
	}
	daemon := NewSidecarTailscaleDaemon(t.namespace.client, t.sidecarName())
	return waitForSidecarLogin(ctx, daemon, t.sidecarCrashed, sidecarLoginTimeout, sidecarLoginPoll)
}

// sidecarCrashed reports whether the sidecar container has died or is
// restart-looping, with a message quoting its recent logs - a rejected
// TS_AUTHKEY presents exactly this way. Inspect failures read as "not
// crashed"; the login wait's daemon probe surfaces those.
func (t *Tailscale) sidecarCrashed(ctx context.Context) (string, bool) {
	info, err := t.namespace.client.ContainerInspect(ctx, t.sidecarName())
	if err != nil || !sidecarWedged(info.State) {
		return "", false
	}
	msg := fmt.Sprintf("the %s sidecar keeps exiting (exit code %d) instead of joining the tailnet", t.sidecarName(), info.State.ExitCode)
	return withRecentLogs(ctx, t.namespace.client, t.sidecarName(), msg), true
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
	return pullImageRef(ctx, t.namespace.client, ref)
}

func (t *Tailscale) removeContainer(ctx context.Context, name string) error {
	return forceRemoveContainer(ctx, t.namespace.client, name)
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

// docktailLabels returns the labels that expose an app as a Tailscale Service
// under its own name. Nil when Tailscale is disabled, so app containers carry
// the labels only while once-docktail is running. The port-80 backend matches
// Once's port-80 assumption; the https service protocol makes the tailnet side
// serve on 443, matching the https:// URLs Once displays; network pins direct
// container-IP proxying to the namespace network the app is attached to.
//
// When funnelPort is set, the same backend is also exposed to the public
// internet via docktail.funnel.* (https on that public port). Each app gets
// its own public port because Funnel serves one endpoint per port per node.
func docktailLabels(appName, network string, enabled bool, funnelPort int) map[string]string {
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
	if funnelPort != 0 {
		labels["docktail.funnel.enable"] = "true"
		labels["docktail.funnel.port"] = "80"
		labels["docktail.funnel.funnel-port"] = strconv.Itoa(funnelPort)
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
// proxies to backend, and returns that endpoint's "host:port" - the public
// address the funnel serves on. Timeout and poll are parameters so tests can
// run the timeout path fast.
func waitForFunnelBackend(ctx context.Context, daemon TailscaleDaemon, backend string, timeout, poll time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		status, err := daemon.ServeStatus(ctx)
		if err == nil {
			if hostPort, ok := status.FunnelBackends[backend]; ok {
				return hostPort, nil
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("funnel did not activate; check that the once-docktail container is running and your tailnet ACL grants the funnel node attribute")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(poll):
		}
	}
}

// funnelURL renders a funnel endpoint's "host:port" as the URL users open,
// with the default HTTPS port elided.
func funnelURL(hostPort string) string {
	if host, ok := strings.CutSuffix(hostPort, ":443"); ok {
		return "https://" + host
	}
	return "https://" + hostPort
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
// problems. daemon, crashed, and the durations are parameters so tests can
// drive the wait with fakes.
func waitForSidecarLogin(ctx context.Context, daemon TailscaleDaemon, crashed func(context.Context) (string, bool), timeout, poll time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if msg, ok := crashed(ctx); ok {
			return errors.New(msg)
		}
		if self, err := daemon.SelfStatus(ctx); err == nil && self.BackendState == "Running" {
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
