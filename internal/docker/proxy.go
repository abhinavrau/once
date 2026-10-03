package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
)

const proxyImage = "abhinavrau/kamal-proxy:once-02"

const (
	stateFileDir  = "/home/kamal-proxy/.config/kamal-proxy"
	stateFileName = "once-state.json"
	stateFilePath = stateFileDir + "/" + stateFileName

	kamalProxyStatePath = stateFileDir + "/kamal-proxy.state"
)

const (
	DefaultHTTPPort    = 80
	DefaultHTTPSPort   = 443
	DefaultMetricsPort = 1318
	deployTimeout      = "120s"
)

type ProxySettings struct {
	HTTPPort    int `json:"httpPort"`
	HTTPSPort   int `json:"httpsPort"`
	MetricsPort int `json:"metricsPort"`
	// Backups holds the install-wide Backup Repo settings. Proxy settings go
	// on the proxy container, and the Proxy is the one container every install
	// has - so install-wide settings live here too (ADR-0014).
	Backups BackupRepoSettings `json:"backups,omitzero"`

	labelExtra
}

func UnmarshalProxySettings(s string) (ProxySettings, error) {
	return decodeLabel[ProxySettings](s)
}

func (s ProxySettings) Marshal() string {
	return encodeLabel(s)
}

type DeployOptions struct {
	AppName         string
	Target          string
	Host            string
	TLS             bool
	HealthCheckPath string
	// TLSCertificatePath and TLSPrivateKeyPath serve a certificate Once
	// supplies instead of one kamal-proxy provisions through ACME. Both are
	// paths inside the proxy container. The recovery handoff uses them to
	// serve on a LAN IP, which no public CA will ever issue for.
	TLSCertificatePath string
	TLSPrivateKeyPath  string
	// AllowFrom restricts the route to requests whose source address falls
	// within one of these CIDRs; any other source gets the same 404 an
	// unknown Host gets (ADR-0016). Empty means the route answers any source,
	// as Public and Recovery Handoff routes must.
	AllowFrom []string
}

type Proxy struct {
	namespace *Namespace
	Settings  *ProxySettings
	image     string
}

// defaultPorts supplies the ports a proxy binds when its settings leave them
// unset - a fresh install's first boot.
var defaultPorts = func() ProxySettings {
	return ProxySettings{HTTPPort: DefaultHTTPPort, HTTPSPort: DefaultHTTPSPort, MetricsPort: DefaultMetricsPort}
}

// OverrideDefaultPortsForTest replaces the ports a proxy booted without
// explicit ones binds, so the integration suite can't take :80/:443 from a
// live install on the same host.
func OverrideDefaultPortsForTest(ports func() ProxySettings) {
	defaultPorts = ports
}

func NewProxy(ns *Namespace) *Proxy {
	return &Proxy{namespace: ns}
}

// OverrideImageForTest points this Proxy at a different image pin, so the
// integration suite can stand in an install running an older image. An empty
// image restores the real pin.
func (p *Proxy) OverrideImageForTest(image string) {
	p.image = image
}

// PinnedImage is the image the proxy must run: the compiled-in pin, unless
// the integration seam overrode it.
func (p *Proxy) PinnedImage() string {
	if p.image != "" {
		return p.image
	}
	return proxyImage
}

// Boot brings the proxy up idempotently, reporting whether an existing proxy
// was rolled because it ran an image other than the pinned one. The roll keeps
// the container's settings label and its named volume - the same replacement
// UpdateSettings performs - so routes, certs and Once's state file survive.
func (p *Proxy) Boot(ctx context.Context, settings ProxySettings) (bool, error) {
	info, err := p.namespace.client.ContainerInspect(ctx, p.containerName())
	if err == nil {
		if info.Config.Image == p.PinnedImage() {
			return false, p.ensureRunning(ctx, info)
		}
		return true, p.roll(ctx, info, withDefaultPorts(settings))
	}
	if !errdefs.IsNotFound(err) {
		return false, fmt.Errorf("inspecting proxy container: %w", err)
	}

	if err := p.ensurePinnedImage(ctx); err != nil {
		return false, err
	}

	settings = withDefaultPorts(settings)
	name := p.containerName()
	metricsPortTCP := nat.Port(fmt.Sprintf("%d/tcp", settings.MetricsPort))

	resp, err := p.namespace.client.ContainerCreate(ctx,
		&container.Config{
			Image: p.PinnedImage(),
			Cmd:   []string{"kamal-proxy", "run", "--metrics-port", fmt.Sprintf("%d", settings.MetricsPort)},
			Labels: map[string]string{
				labelKey: settings.Marshal(),
			},
			ExposedPorts: nat.PortSet{
				"80/tcp":       struct{}{},
				"443/tcp":      struct{}{},
				metricsPortTCP: struct{}{},
			},
		},
		&container.HostConfig{
			PortBindings: nat.PortMap{
				"80/tcp":       []nat.PortBinding{{HostPort: fmt.Sprintf("%d", settings.HTTPPort)}},
				"443/tcp":      []nat.PortBinding{{HostPort: fmt.Sprintf("%d", settings.HTTPSPort)}},
				metricsPortTCP: []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: fmt.Sprintf("%d", settings.MetricsPort)}},
			},
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyAlways},
			LogConfig:     ContainerLogConfig(),
			Mounts: []mount.Mount{
				{
					Type:   mount.TypeVolume,
					Source: name,
					Target: "/home/kamal-proxy/.config/kamal-proxy",
				},
			},
		},
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				p.namespace.name: {},
			},
		},
		nil,
		name,
	)
	if err != nil {
		return false, fmt.Errorf("creating proxy container: %w", err)
	}

	if err := p.namespace.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		if isPortConflict(err) {
			slog.Error("Port conflict starting proxy", "error", err)
			return false, ErrProxyPortInUse
		}
		return false, fmt.Errorf("starting proxy container: %w", err)
	}

	p.Settings = &settings
	return false, nil
}

// UpdateSettings rewrites the proxy's settings label. Docker labels are
// immutable, so the container is removed and booted again with the new
// settings; the named volume stays, so kamal-proxy's routes, certs and Once's
// state file all survive the roll.
func (p *Proxy) UpdateSettings(ctx context.Context, settings ProxySettings) error {
	if err := p.namespace.client.ContainerRemove(ctx, p.containerName(), container.RemoveOptions{Force: true}); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("removing proxy container: %w", err)
	}
	p.Settings = nil
	_, err := p.Boot(ctx, settings)
	return err
}

func (p *Proxy) Destroy(ctx context.Context) error {
	containerName := p.containerName()

	if err := p.namespace.client.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true}); err != nil {
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("removing proxy: %w", err)
		}
	}

	if err := p.namespace.client.VolumeRemove(ctx, containerName, true); err != nil {
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("removing proxy volume: %w", err)
		}
	}

	p.Settings = nil
	return nil
}

func (p *Proxy) Exec(ctx context.Context, cmd []string) error {
	output, err := p.ExecOutput(ctx, cmd)
	if err != nil && output != "" {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(output))
	}
	return err
}

// Remove tears down an app's route, treating an already-absent route as
// success: tailnet apps register none, and exposure transitions must be able
// to clear a route without knowing whether one exists.
func (p *Proxy) Remove(ctx context.Context, appName string) error {
	err := p.Exec(ctx, []string{"kamal-proxy", "remove", appName})
	if err != nil && strings.Contains(err.Error(), "service not found") {
		return nil
	}
	return err
}

func (p *Proxy) Deploy(ctx context.Context, opts DeployOptions) error {
	return p.Exec(ctx, p.deployArgs(opts))
}

// RouteTargets maps each routed app name to the short container ID its route
// targets, as kamal-proxy persists it on every deploy and remove. An install
// with no Proxy, or one that never routed anything, routes nothing.
func (p *Proxy) RouteTargets(ctx context.Context) (map[string]string, error) {
	data, err := p.readFile(ctx, kamalProxyStatePath)
	if errdefs.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseRouteTargets(data)
}

func (p *Proxy) LoadState(ctx context.Context) (*State, error) {
	data, err := p.readFile(ctx, stateFilePath)
	if errdefs.IsNotFound(err) {
		return &State{}, nil
	}
	if err != nil {
		return nil, err
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decoding state: %w", err)
	}

	return &state, nil
}

func (p *Proxy) SaveState(ctx context.Context, state *State) error {
	containerName := p.containerName()

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	header := &tar.Header{
		Name: stateFileName,
		Mode: 0o644,
		Size: int64(len(data)),
	}
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("writing tar header: %w", err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("writing tar data: %w", err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("closing tar writer: %w", err)
	}

	if err := p.namespace.client.CopyToContainer(ctx, containerName, stateFileDir, &buf, container.CopyToContainerOptions{}); err != nil {
		return fmt.Errorf("copying state to container: %w", err)
	}

	return nil
}

func (p *Proxy) ExecOutput(ctx context.Context, cmd []string) (string, error) {
	result, err := execInContainer(ctx, p.namespace.client, p.containerName(), cmd)
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return result.Stdout + result.Stderr, fmt.Errorf("exec failed with exit code %d", result.ExitCode)
	}
	return result.Stdout, nil
}

// Private

func (p *Proxy) containerName() string {
	return p.namespace.name + "-proxy"
}

// roll replaces a proxy whose image drifted from the pin, booting it again
// with the settings the container already carries.
func (p *Proxy) roll(ctx context.Context, info container.InspectResponse, fallback ProxySettings) error {
	settings, err := rollSettings(info, fallback)
	if err != nil {
		return err
	}
	return p.UpdateSettings(ctx, settings)
}

func (p *Proxy) ensureRunning(ctx context.Context, info container.InspectResponse) error {
	if !info.State.Running {
		if err := p.namespace.client.ContainerStart(ctx, info.ID, container.StartOptions{}); err != nil {
			if isPortConflict(err) {
				slog.Error("Port conflict starting proxy", "error", err)
				return ErrProxyPortInUse
			}
			return fmt.Errorf("starting proxy container: %w", err)
		}
	}

	label := info.Config.Labels[labelKey]
	if label != "" {
		settings, err := UnmarshalProxySettings(label)
		if err != nil {
			return fmt.Errorf("unmarshalling proxy settings: %w", err)
		}
		p.Settings = &settings
	}

	return nil
}

// ensurePinnedImage acquires the pinned image with pull-policy "missing":
// inspect locally first, pull only when absent. Pins are immutable tags, so a
// re-pull gains nothing - and the pin may exist only in the local daemon.
func (p *Proxy) ensurePinnedImage(ctx context.Context) error {
	if _, err := p.namespace.client.ImageInspect(ctx, p.PinnedImage()); err == nil {
		return nil
	}
	reader, err := p.namespace.client.ImagePull(ctx, p.PinnedImage(), image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pulling proxy image: %w", err)
	}
	defer reader.Close()
	_, _ = io.Copy(io.Discard, reader)
	return nil
}

func (p *Proxy) deployArgs(opts DeployOptions) []string {
	args := []string{"kamal-proxy", "deploy", opts.AppName, "--target", opts.Target, "--deploy-timeout", deployTimeout}

	if opts.Host != "" {
		args = append(args, "--host", opts.Host)
	}

	if opts.TLS {
		args = append(args, "--tls")
	}

	if opts.TLSCertificatePath != "" {
		args = append(args,
			"--tls-certificate-path", opts.TLSCertificatePath,
			"--tls-private-key-path", opts.TLSPrivateKeyPath)
	}

	if opts.HealthCheckPath != "" {
		args = append(args, "--health-check-path", opts.HealthCheckPath)
	}

	for _, cidr := range opts.AllowFrom {
		args = append(args, "--allow-from", cidr)
	}

	return args
}

// proxyUID is the uid kamal-proxy runs as inside its own image. Files copied
// in arrive owned by root otherwise, and kamal-proxy cannot read its own
// private key - which surfaces only as "unable to load certificate".
const proxyUID = 1001

// copyFiles writes files into a directory in the proxy container, the way
// SaveState already puts the state file there. They are readable only by
// kamal-proxy itself, because one of them is a private key.
func (p *Proxy) copyFiles(ctx context.Context, dir string, files map[string][]byte) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, data := range files {
		header := &tar.Header{
			Name: name,
			Mode: 0o600,
			Size: int64(len(data)),
			Uid:  proxyUID,
			Gid:  proxyUID,
		}
		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("writing tar header: %w", err)
		}
		if _, err := tw.Write(data); err != nil {
			return fmt.Errorf("writing tar data: %w", err)
		}
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("closing tar writer: %w", err)
	}

	return p.namespace.client.CopyToContainer(ctx, p.containerName(), dir, &buf, container.CopyToContainerOptions{})
}

// readFile passes through not-found for a missing container or file.
func (p *Proxy) readFile(ctx context.Context, path string) ([]byte, error) {
	return readContainerFile(ctx, p.namespace.client, p.containerName(), path)
}

// Helpers

type containerFileReader interface {
	CopyFromContainer(ctx context.Context, containerID, srcPath string) (io.ReadCloser, container.PathStat, error)
}

// readContainerFile reads one file out of a container through the archive
// API, passing through not-found for a missing container or file.
func readContainerFile(ctx context.Context, client containerFileReader, containerName, path string) ([]byte, error) {
	reader, _, err := client.CopyFromContainer(ctx, containerName, path)
	if errdefs.IsNotFound(err) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("copying %s from %s: %w", path, containerName, err)
	}
	defer reader.Close()

	tr := tar.NewReader(reader)
	if _, err := tr.Next(); err != nil {
		return nil, fmt.Errorf("reading %s tar: %w", path, err)
	}
	return io.ReadAll(tr)
}

// parseRouteTargets reads kamal-proxy's routing state, whose targets are
// container short IDs, sometimes with a port. Once routes each service to a
// single target.
func parseRouteTargets(data []byte) (map[string]string, error) {
	var services []struct {
		Name          string   `json:"name"`
		ActiveTargets []string `json:"active_targets"`
	}
	if err := json.Unmarshal(data, &services); err != nil {
		return nil, fmt.Errorf("decoding proxy routing state: %w", err)
	}

	targets := make(map[string]string, len(services))
	for _, service := range services {
		if len(service.ActiveTargets) > 0 {
			target, _, _ := strings.Cut(service.ActiveTargets[0], ":")
			targets[service.Name] = target
		}
	}
	return targets, nil
}

// withDefaultPorts only consults defaultPorts when a port is unset, so a
// proxy that already exists never draws ports it won't bind.
func withDefaultPorts(settings ProxySettings) ProxySettings {
	if settings.HTTPPort != 0 && settings.HTTPSPort != 0 && settings.MetricsPort != 0 {
		return settings
	}
	defaults := defaultPorts()
	if settings.HTTPPort == 0 {
		settings.HTTPPort = defaults.HTTPPort
	}
	if settings.HTTPSPort == 0 {
		settings.HTTPSPort = defaults.HTTPSPort
	}
	if settings.MetricsPort == 0 {
		settings.MetricsPort = defaults.MetricsPort
	}
	return settings
}

// rollSettings picks what a rolled proxy boots with: the settings label the
// existing container carries - an image roll must not change configuration -
// falling back to the settings Boot was called with when there is no label.
func rollSettings(info container.InspectResponse, fallback ProxySettings) (ProxySettings, error) {
	label := info.Config.Labels[labelKey]
	if label == "" {
		return fallback, nil
	}
	settings, err := UnmarshalProxySettings(label)
	if err != nil {
		return ProxySettings{}, fmt.Errorf("unmarshalling proxy settings: %w", err)
	}
	return settings, nil
}
