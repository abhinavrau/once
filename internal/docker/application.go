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
	ErrApplicationExists     = errors.New("application already exists")
	ErrHostnameInUse         = errors.New("hostname already in use")
	ErrHostRequired          = errors.New("host is required")
	ErrInvalidBackup         = errors.New("invalid backup archive")
	ErrImageRequired         = errors.New("image is required")
	ErrBackupPathRelative    = errors.New("backup path must be absolute")
	ErrDataPathInvalid       = errors.New("data path must be absolute and must not be /")
	ErrHealthPathInvalid     = errors.New("health check path must begin with /")
	ErrURLEnvVarInvalid      = errors.New("url env var must not contain =")
	ErrSetupFailed           = errors.New("setup failed")
	ErrPullFailed            = &describedError{
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
	port, err := a.namespace.allocateFunnelPort(a)
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

	return a.Destroy(ctx, removeData)
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

// validateTailnetExposure rejects a tailnet deploy that could never work:
// the host must be a valid Magic DNS label (it is the service name), and
// Tailscale must already be enabled - there is no tailnet to appear on
// otherwise, so point at the fix instead of deploying an unreachable app.
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
	return nil
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
		return TailscaleSettings{DomainSuffix: suffix}.TailnetURL(a.Settings.Host)
	}
	return a.URL()
}

// funnelBackend returns the proxy destination docktail programs for this app's
// Funnel: http://<container-IP-on-namespace-network>:80, per the
// docktail.funnel.port label. Matching it in the daemon's serve config is what
// attributes an active Funnel to this app.
func (a *Application) funnelBackend(ctx context.Context) (string, error) {
	name, err := a.ContainerName(ctx)
	if err != nil {
		return "", err
	}
	info, err := a.namespace.client.ContainerInspect(ctx, name)
	if err != nil {
		return "", fmt.Errorf("inspecting %s: %w", name, err)
	}
	endpoint := info.NetworkSettings.Networks[a.namespace.name]
	if endpoint == nil || endpoint.IPAddress == "" {
		return "", fmt.Errorf("app %s has no IP address on network %s", a.Settings.Name, a.namespace.name)
	}
	return "http://" + endpoint.IPAddress + ":80", nil
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
		a.containerConfig(env, tailscaleEnabled),
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

	if err := a.registerRouting(ctx, resp.ID); err != nil {
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
// Local and public apps register a host route with the Proxy; tailnet apps are
// advertised by Docktail via their container labels instead, so any proxy
// route left over from a previous exposure is torn down. Exactly one path
// exists per exposure - never both.
func (a *Application) registerRouting(ctx context.Context, containerID string) error {
	if a.Settings.Exposure == ExposureTailnet {
		if err := a.namespace.Proxy().Remove(ctx, a.Settings.Name); err != nil {
			return fmt.Errorf("removing proxy route: %w", err)
		}
		return nil
	}

	if err := a.namespace.Proxy().Deploy(ctx, DeployOptions{
		AppName:         a.Settings.Name,
		Target:          containerID[:12],
		Host:            a.Settings.Host,
		TLS:             a.Settings.TLSEnabled(),
		HealthCheckPath: a.Settings.Profile.HealthCheckPath(),
	}); err != nil {
		if strings.Contains(err.Error(), "target not healthy") || strings.Contains(err.Error(), "deploy timed out") {
			slog.Error("Application failed to start", "app", a.Settings.Name, "error", err)
			return ErrAppNotStarted
		}
		return fmt.Errorf("registering with proxy: %w", err)
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

func (a *Application) containerConfig(env []string, tailscaleEnabled bool) *container.Config {
	labels := map[string]string{labelKey: a.Settings.Marshal()}
	// Only tailnet-exposed apps carry docktail labels, advertised under the
	// Host (the service name). Off the tailnet there is no Funnel either.
	exposed := tailscaleEnabled && a.Settings.Exposure == ExposureTailnet
	maps.Copy(labels, docktailLabels(a.Settings.Host, a.namespace.name, exposed, a.Settings.ActiveFunnelPort()))
	return &container.Config{
		Image:  a.Settings.Image,
		Labels: labels,
		Env:    env,
	}
}
