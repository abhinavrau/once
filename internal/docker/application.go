package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
)

var (
	ErrHostnameInUse      = errors.New("hostname already in use")
	ErrHostRequired       = errors.New("host is required")
	ErrInvalidBackup      = errors.New("invalid backup archive")
	ErrImageRequired      = errors.New("image is required")
	ErrBackupPathRelative = errors.New("backup path must be absolute")
	ErrDataPathInvalid    = errors.New("data path must be absolute and must not be /")
	ErrHealthPathInvalid  = errors.New("health check path must begin with /")
	ErrURLEnvVarInvalid   = errors.New("url env var must not contain =")
	ErrSetupFailed        = errors.New("setup failed")
	ErrPullFailed         = &describedError{
		msg:         "pull failed",
		description: "Failed to download the application image. Check that the image name is correct and try again.",
	}
	ErrDeployFailed          = errors.New("deploy failed")
	ErrTailscaleNotEnabled   = errors.New("tailnet exposure requires Tailscale, which is not enabled")
	ErrFunnelRequiresTailnet = errors.New("funnel requires tailnet exposure; switch the app with `once update <host> --expose tailnet` first")
	ErrVerificationFailed    = &describedError{
		msg:         "verification failed",
		description: "The application couldn't be verified. Please check that you have a valid DNS record set up.",
	}
	ErrUnpauseFailed = errors.New("failed to unpause container after backup")
	// ErrBackupInProgress is the host flock's refusal, reported alike by the
	// manual export and by a restore: both write the Staging Directory, and a
	// backup pauses an application.
	ErrBackupInProgress = errors.New("a backup is already in progress; try again when it finishes")
)

const (
	AutomaticTaskInterval = 24 * time.Hour
	// DefaultHealthCheckPath is the path probed when an app's Profile does not
	// declare one; read through AppProfile.HealthCheckPath, never directly.
	DefaultHealthCheckPath = "/up"
	httpVerifyTimeout      = 30 * time.Second
)

// DefaultAppVolumeMountTargets are the paths the app data volume is mounted at
// when an app's Profile does not declare a data path; read through
// AppProfile.MountTargets, never directly. The first entry is the primary path
// used for backups.
var DefaultAppVolumeMountTargets = []string{"/storage", "/rails/storage"}

type Application struct {
	namespace    *Namespace
	Settings     ApplicationSettings
	Running      bool
	RunningSince time.Time
}

func NewApplication(ns *Namespace, settings ApplicationSettings) *Application {
	return &Application{
		namespace: ns,
		Settings:  settings,
	}
}

func (a *Application) ContainerName(ctx context.Context) (string, error) {
	containers, err := a.namespace.client.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return "", err
	}

	for _, c := range containers {
		if len(c.Names) == 0 {
			continue
		}
		name := strings.TrimPrefix(c.Names[0], "/")
		if a.namespace.containerAppName(name) == a.Settings.Name {
			return name, nil
		}
	}

	return "", fmt.Errorf("no container found for app %s", a.Settings.Name)
}

func (a *Application) Volume(ctx context.Context) (*ApplicationVolume, error) {
	vol, err := FindVolume(ctx, a.namespace, a.Settings.Name)
	if err == nil {
		return vol, nil
	}
	if !errors.Is(err, ErrVolumeNotFound) {
		return nil, err
	}

	skb, err := generateSecretKeyBase()
	if err != nil {
		return nil, fmt.Errorf("generating secret key base: %w", err)
	}
	vapidPub, vapidPriv, err := generateVAPIDKeyPair()
	if err != nil {
		return nil, fmt.Errorf("generating VAPID key pair: %w", err)
	}
	return CreateVolume(ctx, a.namespace, a.Settings.Name, ApplicationVolumeSettings{
		SecretKeyBase:   skb,
		VAPIDPublicKey:  vapidPub,
		VAPIDPrivateKey: vapidPriv,
	})
}

// URL is the app's proxy-routed address. Tailnet apps have none: their only
// address is the tailnet service URL, computed via TailscaleSettings.TailnetURL.
func (a *Application) URL() string {
	if a.Settings.Host == "" || a.Settings.Exposure == ExposureTailnet {
		return ""
	}

	scheme := "http"
	defaultPort := 80
	if a.Settings.TLSEnabled() {
		scheme = "https"
		defaultPort = 443
	}

	base := scheme + "://" + a.Settings.Host

	if a.namespace == nil {
		return base
	}

	proxy := a.namespace.Proxy()
	if proxy.Settings == nil {
		return base
	}

	port := proxy.Settings.HTTPPort
	if a.Settings.TLSEnabled() {
		port = proxy.Settings.HTTPSPort
	}

	if port != 0 && port != defaultPort {
		return base + ":" + strconv.Itoa(port)
	}
	return base
}

func (a *Application) Stop(ctx context.Context) error {
	name, err := a.ContainerName(ctx)
	if err != nil {
		return err
	}

	return a.namespace.client.ContainerStop(ctx, name, container.StopOptions{})
}

func (a *Application) Start(ctx context.Context) error {
	name, err := a.ContainerName(ctx)
	if err != nil {
		return err
	}

	return a.namespace.client.ContainerStart(ctx, name, container.StartOptions{})
}

func (a *Application) Update(ctx context.Context, progress DeployProgressCallback) (bool, error) {
	changed, err := a.pullImage(ctx, progress)
	if err != nil {
		a.saveOperationResult(ctx, func(s *State) { s.RecordUpdate(a.Settings.Name, err) })
		return false, err
	}

	if !changed {
		a.saveOperationResult(ctx, func(s *State) { s.RecordUpdate(a.Settings.Name, nil) })
		return false, nil
	}

	vol, err := a.Volume(ctx)
	if err != nil {
		err = fmt.Errorf("getting volume: %w", err)
		a.saveOperationResult(ctx, func(s *State) { s.RecordUpdate(a.Settings.Name, err) })
		return false, err
	}

	err = a.deployWithVolume(ctx, vol, progress)
	a.saveOperationResult(ctx, func(s *State) { s.RecordUpdate(a.Settings.Name, err) })
	return true, err
}

func (a *Application) Deploy(ctx context.Context, progress DeployProgressCallback) error {
	if a.Settings.Host == "" {
		return ErrHostRequired
	}

	if err := a.validateTailnetExposure(ctx); err != nil {
		return err
	}

	// Leaving the tailnet revokes any Funnel: a surviving expiry would
	// silently re-publish the app if it returned within the window.
	if a.Settings.Exposure != ExposureTailnet {
		a.Settings.FunnelExpiresAt = nil
		a.Settings.FunnelPort = 0
	}

	if _, err := a.pullImage(ctx, progress); err != nil {
		return err
	}

	vol, err := a.Volume(ctx)
	if err != nil {
		return fmt.Errorf("getting volume: %w", err)
	}

	return a.deployWithVolume(ctx, vol, progress)
}

// Roll recreates the app's container in place (zero-downtime, via the normal
// deploy path) so it picks up label changes such as docktail.* injection. It
// reuses the running image and the existing volume - no image pull.
func (a *Application) Roll(ctx context.Context, progress DeployProgressCallback) error {
	vol, err := a.Volume(ctx)
	if err != nil {
		return fmt.Errorf("getting volume: %w", err)
	}
	return a.deployWithVolume(ctx, vol, progress)
}

// EnableFunnel grants temporary public access via Tailscale Funnel. Setting
// FunnelExpiresAt and recreating the container writes both the expiry (in the
// once label) and the docktail.funnel.* labels in one atomic recreation, so
// the funnel state and its expiry can never drift apart. Only a tailnet-exposed
// app has a Service to funnel; permanent public access is `--expose public`.
func (a *Application) EnableFunnel(ctx context.Context, expiresAt time.Time) error {
	if a.Settings.Exposure != ExposureTailnet {
		return ErrFunnelRequiresTailnet
	}
	port, err := a.namespace.Tailscale().FunnelPort(ctx, a)
	if err != nil {
		return err
	}
	a.Settings.FunnelExpiresAt = &expiresAt
	a.Settings.FunnelPort = port
	return a.Roll(ctx, nil)
}

// DisableFunnel revokes public access immediately by clearing FunnelExpiresAt
// and recreating the container without the funnel labels.
func (a *Application) DisableFunnel(ctx context.Context) error {
	a.Settings.FunnelExpiresAt = nil
	a.Settings.FunnelPort = 0
	return a.Roll(ctx, nil)
}

func (a *Application) VerifyHTTPOrRemove(ctx context.Context) error {
	if err := a.verifyHTTP(ctx); err != nil {
		if cleanupErr := a.Remove(context.Background(), true); cleanupErr != nil {
			slog.Error("Failed to clean up after verification failure", "app", a.Settings.Name, "error", cleanupErr)
		}
		return err
	}

	return nil
}

func (a *Application) Remove(ctx context.Context, removeData bool) error {
	if err := a.namespace.Proxy().Remove(ctx, a.Settings.Name); err != nil {
		return fmt.Errorf("removing from proxy: %w", err)
	}
	if err := a.Destroy(ctx, removeData); err != nil {
		return err
	}

	a.removeTailnetDevice(ctx)
	return nil
}

func (a *Application) Destroy(ctx context.Context, destroyVolumes bool) error {
	containers, err := a.namespace.client.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return err
	}

	for _, c := range containers {
		for _, name := range c.Names {
			name = strings.TrimPrefix(name, "/")
			if a.namespace.containerAppName(name) == a.Settings.Name {
				if err := a.namespace.client.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
					return fmt.Errorf("removing container: %w", err)
				}
				break
			}
		}
	}

	if destroyVolumes {
		vol, err := FindVolume(ctx, a.namespace, a.Settings.Name)
		if err != nil && !errors.Is(err, ErrVolumeNotFound) {
			return fmt.Errorf("getting volume: %w", err)
		}
		if vol != nil {
			if err := vol.Destroy(ctx); err != nil {
				return err
			}
		}
	}

	return nil
}

// Private

// removeTailnetDevice releases the tailnet identity a tailnet app's topology
// created for it, best effort, so the app leaves no orphan behind it. Remove
// only, never Destroy: Destroy also cleans up after a refused deploy or
// restore, whose Host may name a device this app never owned.
func (a *Application) removeTailnetDevice(ctx context.Context) {
	if a.Settings.Exposure != ExposureTailnet {
		return
	}
	a.namespace.Tailscale().RemoveApp(ctx, a.Settings.Host)
}

// validateTailnetExposure rejects a tailnet deploy that could never work:
// the host must be a valid Magic DNS label (it is the service/node name),
// Tailscale must already be enabled - there is no tailnet to appear on
// otherwise - and, under the active topology, the host must clear whatever
// else that topology requires (nodes additionally refuses a host already
// held by another tailnet device).
func (a *Application) validateTailnetExposure(ctx context.Context) error {
	if a.Settings.Exposure != ExposureTailnet {
		return nil
	}
	if err := ValidateTailscaleServiceName(a.Settings.Host); err != nil {
		return err
	}
	enabled, err := a.namespace.Tailscale().Enabled(ctx)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrTailscaleNotEnabled
	}
	return a.namespace.Tailscale().ValidateHost(ctx, a.Settings.Host)
}

// resolvedURL is the address the Profile's URLEnvVar is filled with: the
// proxy-routed URL, or the tailnet service URL under Tailnet exposure. Best
// effort - a live daemon read must never fail a deploy - so it may be empty,
// which Deploy surfaces as a warning rather than an error.
func (a *Application) resolvedURL(ctx context.Context, tailscaleEnabled bool) string {
	if a.Settings.Exposure == ExposureTailnet {
		if !tailscaleEnabled {
			return ""
		}
		suffix, _ := a.namespace.Tailscale().DomainSuffix(ctx)
		// ponytail: read, don't wait. The node holding a deduped name
		// (campfire-1) reports it here on every deploy but the very first,
		// where its container - and so its node - does not exist yet and the
		// composed name is the only guess available. Waiting for the node to
		// join would block every first deploy on a value only some apps use.
		fqdns, _ := a.namespace.Tailscale().NodeFQDNs(ctx)
		return TailscaleSettings{DomainSuffix: suffix}.TailnetURLFor(a.Settings.Host, fqdns)
	}
	return a.URL()
}

func (a *Application) saveOperationResult(ctx context.Context, record func(*State)) {
	state, err := a.namespace.LoadState(ctx)
	if err != nil {
		return
	}
	record(state)
	a.namespace.SaveState(ctx, state)
}

func (a *Application) pullImage(ctx context.Context, progress DeployProgressCallback) (bool, error) {
	opts := image.PullOptions{RegistryAuth: registryAuthFor(a.Settings.Image)}
	reader, err := a.namespace.client.ImagePull(ctx, a.Settings.Image, opts)
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrPullFailed, err)
	}
	defer reader.Close()

	if progress != nil {
		tracker := newPullProgressTracker(progress)
		if err := tracker.Track(reader); err != nil {
			return false, fmt.Errorf("%w: %w", ErrPullFailed, err)
		}
	} else {
		if _, err := io.Copy(io.Discard, reader); err != nil {
			return false, fmt.Errorf("%w: %w", ErrPullFailed, err)
		}
	}

	pulledInspect, err := a.namespace.client.ImageInspect(ctx, a.Settings.Image)
	if err != nil {
		return false, fmt.Errorf("%w: inspecting image after pull: %w", ErrPullFailed, err)
	}

	return pulledInspect.ID != a.runningImageID(ctx), nil
}

func (a *Application) runningImageID(ctx context.Context) string {
	name, err := a.ContainerName(ctx)
	if err != nil {
		return ""
	}
	info, err := a.namespace.client.ContainerInspect(ctx, name)
	if err != nil {
		return ""
	}
	return info.Image
}

func (a *Application) deployWithVolume(ctx context.Context, vol *ApplicationVolume, progress DeployProgressCallback) error {
	if progress != nil {
		progress(DeployProgress{Stage: DeployStageStarting})
	}

	id, err := ContainerRandomID()
	if err != nil {
		return fmt.Errorf("generating container id: %w", err)
	}

	containerName := fmt.Sprintf("%s-app-%s-%s", a.namespace.name, a.Settings.Name, id)

	tailscaleEnabled, err := a.namespace.Tailscale().Enabled(ctx)
	if err != nil {
		return fmt.Errorf("checking tailscale: %w", err)
	}
	// Only tailnet-exposed apps carry tailnet labels, advertised under the
	// Host. Off the tailnet there is no Funnel either.
	exposedOnTailnet := tailscaleEnabled && a.Settings.Exposure == ExposureTailnet
	tsLabels, err := a.namespace.Tailscale().AppLabels(ctx, exposedOnTailnet, a.Settings.Host, a.namespace.name, a.Settings.ActiveFunnelPort())
	if err != nil {
		return fmt.Errorf("resolving tailnet labels: %w", err)
	}
	joined := a.joinedNodeFQDN(ctx, exposedOnTailnet)

	env := a.Settings.BuildEnv(vol.Settings, a.resolvedURL(ctx, tailscaleEnabled))

	hostConfig := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyAlways},
		LogConfig:     ContainerLogConfig(),
		Mounts:        a.volumeMounts(vol),
	}
	hostConfig.Resources = container.Resources{
		Memory:   int64(a.Settings.Resources.MemoryMB) * 1024 * 1024,
		NanoCPUs: int64(a.Settings.Resources.CPUs) * 1e9,
	}

	resp, err := a.namespace.client.ContainerCreate(ctx,
		a.containerConfig(env, tsLabels),
		hostConfig,
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				a.namespace.name: {},
			},
		},
		nil,
		containerName,
	)
	if err != nil {
		return fmt.Errorf("creating container: %w", err)
	}

	if err := a.namespace.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		a.namespace.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return fmt.Errorf("starting container: %w", err)
	}

	if err := a.registerRouting(ctx, resp.ID, tailscaleEnabled, joined); err != nil {
		a.namespace.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return err
	}

	if err := a.removeContainersExcept(ctx, containerName); err != nil {
		return fmt.Errorf("removing old containers: %w", err)
	}

	if progress != nil {
		progress(DeployProgress{Stage: DeployStageFinished})
	}

	return nil
}

// registerRouting puts the new container on the app's single routing path.
// Local and public apps always register a host route with the Proxy. Tailnet
// apps depend on the active topology (ADR-0015): under nodes, traffic goes
// through the Proxy too - TSDProxy forwards to it, not the app container - so
// the app registers a route on its own tailnet Host with TLS off (TSDProxy
// terminates TLS); under services (or Tailscale disabled), Docktail
// advertises the app directly and any proxy route left over from a previous
// exposure is torn down instead. Exactly one path exists per exposure/
// topology combination - never both.
func (a *Application) registerRouting(ctx context.Context, containerID string, tailscaleEnabled bool, joined string) error {
	if a.Settings.Exposure == ExposureTailnet {
		if tailscaleEnabled {
			viaProxy, err := a.namespace.Tailscale().RoutesTailnetAppsViaProxy(ctx)
			if err != nil {
				return err
			}
			if viaProxy {
				return a.registerNodesRoute(ctx, containerID, joined)
			}
		}
		if err := a.namespace.Proxy().Remove(ctx, a.Settings.Name); err != nil {
			return fmt.Errorf("removing proxy route: %w", err)
		}
		return nil
	}

	return a.registerProxyRoute(ctx, containerID)
}

// reregisterRoute re-issues the app's Proxy route at its currently running
// container, through the same registration a deploy uses. The caller has
// already decided the app's route lives on the Proxy (reregisterTargets).
// A tailnet route is re-resolved on the FQDN its running node reports, so a
// route registered on a composed Host heals onto the name the node holds.
func (a *Application) reregisterRoute(ctx context.Context) error {
	name, err := a.ContainerName(ctx)
	if err != nil {
		return err
	}
	info, err := a.namespace.client.ContainerInspect(ctx, name)
	if err != nil {
		return fmt.Errorf("inspecting container: %w", err)
	}

	if a.Settings.Exposure == ExposureTailnet {
		joined, err := a.namespace.Tailscale().NodeRouteHost(ctx, a.Settings.Host)
		if err != nil {
			return err
		}
		return a.registerNodesRoute(ctx, info.ID, joined)
	}
	return a.registerProxyRoute(ctx, info.ID)
}

// registerProxyRoute registers the app's host route with the Proxy: the one
// path shared by a deploy and a post-roll re-registration, so every route
// option Once emits comes from here. A Local route answers only a Trusted
// Source - the once network's subnets, read from Docker at registration time
// (ADR-0016); a Public route answers any source.
func (a *Application) registerProxyRoute(ctx context.Context, containerID string) error {
	opts := DeployOptions{
		AppName:         a.Settings.Name,
		Target:          containerID[:12],
		Host:            a.Settings.Host,
		TLS:             a.Settings.TLSEnabled(),
		HealthCheckPath: a.Settings.Profile.HealthCheckPath(),
	}
	if a.Settings.Exposure == ExposureLocal {
		subnets, err := a.namespace.NetworkSubnets(ctx)
		if err != nil {
			return fmt.Errorf("resolving trusted sources: %w", err)
		}
		opts.AllowFrom = subnets
	}

	if err := a.namespace.Proxy().Deploy(ctx, opts); err != nil {
		if strings.Contains(err.Error(), "target not healthy") || strings.Contains(err.Error(), "deploy timed out") {
			slog.Error("Application failed to start", "app", a.Settings.Name, "error", err)
			return ErrAppNotStarted
		}
		return fmt.Errorf("registering with proxy: %w", err)
	}
	return nil
}

// joinedNodeFQDN is the FQDN the app's node already answers on, read once
// before its container is replaced - TSDProxy drops the proxy from its API
// mid-swap, so this is a Roll's one chance to see the name its node holds.
// Empty means the node joins with this deploy (or TSDProxy is not reporting
// it), so the registration waits for the name and refuses a deduped one.
func (a *Application) joinedNodeFQDN(ctx context.Context, exposedOnTailnet bool) string {
	if !exposedOnTailnet {
		return ""
	}
	fqdns, _ := a.namespace.Tailscale().NodeFQDNs(ctx)
	return fqdns[a.Settings.Host]
}

// registerNodesRoute registers the Proxy route a nodes-topology tailnet app
// answers on: its own tailnet Host ({app}.{suffix}), TLS off since TSDProxy
// already terminated it with the tailnet's own certificate. joined is as
// registerNodesProxyRoute takes it.
func (a *Application) registerNodesRoute(ctx context.Context, containerID, joined string) error {
	if err := registerNodesProxyRoute(ctx, a.namespace, a.Settings.Name, a.Settings.Host, containerID, a.Settings.Profile.HealthCheckPath(), joined); err != nil {
		if strings.Contains(err.Error(), "target not healthy") || strings.Contains(err.Error(), "deploy timed out") {
			slog.Error("Application failed to start", "app", a.Settings.Name, "error", err)
			return ErrAppNotStarted
		}
		return err
	}
	return nil
}

func (a *Application) verifyHTTP(ctx context.Context) error {
	url := a.URL()
	if url == "" {
		return nil
	}

	client := &http.Client{Timeout: httpVerifyTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+a.Settings.Profile.HealthCheckPath(), nil)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrVerificationFailed, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrVerificationFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%w: unexpected status %d from %s", ErrVerificationFailed, resp.StatusCode, url)
	}

	return nil
}

func (a *Application) removeContainersExcept(ctx context.Context, keep string) error {
	containers, err := a.namespace.client.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return err
	}

	for _, c := range containers {
		if len(c.Names) == 0 {
			continue
		}
		name := strings.TrimPrefix(c.Names[0], "/")
		if a.namespace.containerAppName(name) == a.Settings.Name && name != keep {
			if err := a.namespace.client.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
				return err
			}
		}
	}

	return nil
}

func (a *Application) volumeMounts(vol *ApplicationVolume) []mount.Mount {
	var mounts []mount.Mount
	for _, target := range a.Settings.Profile.MountTargets() {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeVolume,
			Source: vol.Name(),
			Target: target,
		})
	}
	return mounts
}

// containerConfig assembles the app container's config from its own settings
// label plus tsLabels - the tailnet labels the facade already resolved for
// the active topology (nil when Tailscale is disabled or the app isn't
// tailnet-exposed).
func (a *Application) containerConfig(env []string, tsLabels map[string]string) *container.Config {
	labels := map[string]string{labelKey: a.Settings.Marshal()}
	maps.Copy(labels, tsLabels)
	return &container.Config{
		Image:  a.Settings.Image,
		Labels: labels,
		Env:    env,
	}
}
