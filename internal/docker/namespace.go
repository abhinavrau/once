package docker

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"

	"github.com/basecamp/once/internal/logging"
)

const DefaultNamespace = "once"

var ErrInvalidNamespace = errors.New("invalid namespace: must contain only lowercase letters, digits, and hyphens, and must not start with a hyphen")

type Namespace struct {
	name         string
	client       *client.Client
	proxy        *Proxy
	tailscale    *Tailscale
	admin        *Admin
	applications []*Application
}

type NamespaceOption func(*Namespace)

func WithApplications(apps ...ApplicationSettings) NamespaceOption {
	return func(ns *Namespace) {
		for _, s := range apps {
			ns.addApplication(s)
		}
	}
}

func NewNamespace(name string, opts ...NamespaceOption) (*Namespace, error) {
	if name == "" {
		name = DefaultNamespace
	}

	if !validNamespace.MatchString(name) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidNamespace, name)
	}

	c, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation(), withLoggingTransport())
	if err != nil {
		return nil, err
	}

	ns := &Namespace{
		name:   name,
		client: c,
	}
	ns.proxy = NewProxy(ns)
	ns.tailscale = NewTailscale(ns)
	ns.admin = NewAdmin(ns)

	for _, opt := range opts {
		opt(ns)
	}

	return ns, nil
}

func RestoreNamespace(ctx context.Context, name string) (*Namespace, error) {
	ns, err := NewNamespace(name)
	if err != nil {
		return nil, err
	}

	if err := ns.restoreState(ctx); err != nil {
		return nil, err
	}

	return ns, nil
}

func (n *Namespace) Name() string {
	return n.name
}

func (n *Namespace) addApplication(settings ApplicationSettings) *Application {
	app := NewApplication(n, settings)
	n.applications = append(n.applications, app)
	n.sortApplications()
	return app
}

func (n *Namespace) Proxy() *Proxy {
	return n.proxy
}

func (n *Namespace) Tailscale() *Tailscale {
	return n.tailscale
}

func (n *Namespace) Admin() *Admin {
	return n.admin
}

func (n *Namespace) Application(name string) *Application {
	for _, app := range n.applications {
		if app.Settings.Name == name {
			return app
		}
	}
	return nil
}

func (n *Namespace) Applications() []*Application {
	return n.applications
}

func (n *Namespace) ApplicationByHost(host string) *Application {
	for _, app := range n.applications {
		if app.Settings.Host == host {
			return app
		}
	}
	return nil
}

func (n *Namespace) HostInUse(host string) bool {
	return n.ApplicationByHost(host) != nil
}

func (n *Namespace) HostInUseByAnother(host string, excludeApp string) bool {
	for _, app := range n.applications {
		if app.Settings.Host == host && app.Settings.Name != excludeApp {
			return true
		}
	}
	return false
}

func (n *Namespace) UniqueName(base string) (string, error) {
	for {
		id, err := randomID(6)
		if err != nil {
			return "", err
		}
		candidate := fmt.Sprintf("%s-%s", base, id)
		if n.Application(candidate) == nil {
			return candidate, nil
		}
	}
}

func (n *Namespace) Setup(ctx context.Context) error {
	if err := n.EnsureNetwork(ctx); err != nil {
		return err
	}

	return n.proxy.Boot(ctx, ProxySettings{})
}

func (n *Namespace) EnsureNetwork(ctx context.Context) error {
	networks, err := n.client.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		return err
	}

	for _, net := range networks {
		if net.Name == n.name {
			return nil
		}
	}

	_, err = n.client.NetworkCreate(ctx, n.name, network.CreateOptions{
		Driver: "bridge",
	})
	return err
}

// EnableTailscale is the full enable lifecycle shared by the CLI and TUI:
// validate the exposed app names, the host-mode pre-flight (when the
// effective mode is host), and the credentials, bring up the network,
// boot the Docktail topology with the given credentials, boot once-admin so
// it is exposed as a Service too, then verify every expected service is
// advertised and approved before reporting success. The returned warnings
// name any tailnet machines whose Magic DNS entries shadow a service, and
// lead with the --mode host notice when a fresh enable defaulted to the
// sidecar alongside a host tailscaled.
func (n *Namespace) EnableTailscale(ctx context.Context, settings TailscaleSettings) ([]string, error) {
	if err := ValidateTailscaleMode(settings.Mode); err != nil {
		return nil, err
	}
	exposed := n.exposedTailscaleServices()
	for _, name := range exposed {
		if err := ValidateTailscaleServiceName(name); err != nil {
			return nil, err
		}
	}
	if err := n.admin.RequireDaemon(); err != nil {
		return nil, err
	}
	settings.Tag = settings.normalizeTag()
	mode, err := n.tailscale.effectiveMode(ctx, settings.Mode)
	if err != nil {
		return nil, err
	}
	if mode == TailscaleModeHost {
		if err := validateHostModePreflight(ctx, tailscaleAPIBaseURL, NewHostTailscaleDaemon(), settings); err != nil {
			return nil, err
		}
	}
	if err := validateTailscaleCredentials(ctx, settings); err != nil {
		return nil, err
	}
	if err := n.EnsureNetwork(ctx); err != nil {
		return nil, fmt.Errorf("ensuring network: %w", err)
	}
	notice, err := n.tailscale.Enable(ctx, settings)
	if err != nil {
		return nil, fmt.Errorf("enabling Tailscale: %w", err)
	}
	if err := n.admin.Boot(ctx); err != nil {
		return nil, fmt.Errorf("booting once-admin: %w", err)
	}
	var warnings []string
	if notice != "" {
		warnings = append(warnings, notice)
	}
	warnings = append(warnings, n.tailscale.ShadowWarnings(ctx, exposed)...)
	return warnings, n.tailscale.VerifyServices(ctx, n.expectedTailscaleServices())
}

// DisableTailscale is the full disable lifecycle shared by the CLI and TUI:
// remove once-admin, then the Docktail topology (keeping the sidecar's state
// volume).
func (n *Namespace) DisableTailscale(ctx context.Context) error {
	if err := n.admin.Destroy(ctx); err != nil {
		return fmt.Errorf("removing once-admin: %w", err)
	}
	if err := n.tailscale.Disable(ctx); err != nil {
		return fmt.Errorf("disabling Tailscale: %w", err)
	}
	return nil
}

// Teardown is best effort: every removal is attempted even when an earlier one
// fails, so one hiccup can't strand the rest of the namespace's containers.
func (n *Namespace) Teardown(ctx context.Context, destroyVolumes bool) error {
	var errs []error
	for _, app := range n.applications {
		errs = append(errs, app.Destroy(ctx, destroyVolumes))
	}
	errs = append(errs,
		n.proxy.Destroy(ctx),
		n.tailscale.Destroy(ctx),
		n.admin.Destroy(ctx),
		n.client.NetworkRemove(ctx, n.name),
	)
	return errors.Join(errs...)
}

func (n *Namespace) Refresh(ctx context.Context) error {
	n.applications = nil
	return n.restoreState(ctx)
}

// RollApplications recreates every running app container (zero-downtime) so they
// pick up the current docktail.* label state. It refreshes from Docker first so
// the roll covers apps regardless of whether they were preloaded — the retrofit
// behind once tailscale enable/disable.
func (n *Namespace) RollApplications(ctx context.Context) error {
	if err := n.Refresh(ctx); err != nil {
		return err
	}
	for _, app := range n.applications {
		if !app.Running {
			continue
		}
		if err := app.Roll(ctx, nil); err != nil {
			return fmt.Errorf("rolling %s: %w", app.Settings.Name, err)
		}
	}
	return nil
}

func (n *Namespace) DockerRootDir(ctx context.Context) (string, error) {
	info, err := n.client.Info(ctx)
	if err != nil {
		return "", err
	}
	return info.DockerRootDir, nil
}

func (n *Namespace) EventWatcher() *EventWatcher {
	return NewEventWatcher(n.client, n.name)
}

func (n *Namespace) ApplicationExists(ctx context.Context, name string) (bool, error) {
	containers, err := n.client.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return false, err
	}

	for _, c := range containers {
		for _, cname := range c.Names {
			cname = strings.TrimPrefix(cname, "/")
			if n.containerAppName(cname) == name {
				return true, nil
			}
		}
	}

	return false, nil
}

func (n *Namespace) LoadState(ctx context.Context) (*State, error) {
	return n.proxy.LoadState(ctx)
}

func (n *Namespace) SaveState(ctx context.Context, state *State) error {
	return n.proxy.SaveState(ctx, state)
}

func (n *Namespace) Restore(ctx context.Context, r io.Reader) (*Application, error) {
	appSettings, volSettings, volumeData, err := n.parseBackup(r)
	if err != nil {
		return nil, fmt.Errorf("parsing backup: %w", err)
	}

	if n.HostInUse(appSettings.Host) {
		return nil, ErrHostnameInUse
	}

	name, err := n.UniqueName(NameFromImageRef(appSettings.Image))
	if err != nil {
		return nil, fmt.Errorf("generating app name: %w", err)
	}
	appSettings.Name = name

	app := NewApplication(n, appSettings)
	if err := app.Restore(ctx, volSettings, volumeData); err != nil {
		if cleanupErr := app.Destroy(context.Background(), true); cleanupErr != nil {
			slog.Error("Failed to clean up after restore failure", "app", appSettings.Name, "error", cleanupErr)
		}
		return nil, err
	}

	if err := n.Refresh(ctx); err != nil {
		slog.Error("Failed to refresh namespace after restore", "app", appSettings.Name, "error", err)
	}

	if restored := n.Application(appSettings.Name); restored != nil {
		return restored, nil
	}
	return app, nil
}

// containerAppName extracts the application name from a container name
// matching the pattern {namespace}-app-{appName}-{id}. Returns "" if the
// container name doesn't match.
func (n *Namespace) containerAppName(containerName string) string {
	after, ok := strings.CutPrefix(containerName, n.name+"-app-")
	if !ok {
		return ""
	}
	appName, _, ok := cutLast(after, "-")
	if !ok {
		return ""
	}
	return appName
}

// Private

// expectedTailscaleServices lists the services that should exist on the
// tailnet after enable: one per running tailnet-exposed application (under
// its Host, the service name), plus the Once Admin Web App. Stopped apps
// aren't advertised by Docktail, so they can't be verified.
func (n *Namespace) expectedTailscaleServices() []string {
	expected := []string{"once-admin"}
	for _, app := range n.applications {
		if app.Running && app.Settings.Exposure == ExposureTailnet {
			expected = append(expected, app.Settings.Host)
		}
	}
	return expected
}

// exposedTailscaleServices lists every service name that appears on the
// tailnet while Tailscale is enabled: each tailnet-exposed application
// (running or not - a stopped app is advertised as soon as it starts) plus
// the Once Admin Web App.
func (n *Namespace) exposedTailscaleServices() []string {
	names := []string{"once-admin"}
	for _, app := range n.applications {
		if app.Settings.Exposure == ExposureTailnet {
			names = append(names, app.Settings.Host)
		}
	}
	return names
}

type appCandidate struct {
	app     *Application
	created int64
}

func (n *Namespace) restoreState(ctx context.Context) error {
	containers, err := n.client.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return err
	}

	proxyPrefix := n.name + "-proxy"
	appPrefix := n.name + "-app-"

	// Use a map to deduplicate apps by name, preferring the most recently created container
	appsByName := make(map[string]appCandidate)

	for _, c := range containers {
		for _, name := range c.Names {
			name = strings.TrimPrefix(name, "/")

			if name == proxyPrefix {
				label := c.Labels[labelKey]
				if label != "" {
					settings, err := UnmarshalProxySettings(label)
					if err != nil {
						return err
					}
					n.proxy.Settings = &settings
				}
				break
			}

			if strings.HasPrefix(name, appPrefix) {
				label := c.Labels[labelKey]
				if label != "" {
					settings, err := UnmarshalApplicationSettings(label)
					if err != nil {
						return err
					}
					app := NewApplication(n, settings)
					app.Running = c.State == "running"
					if app.Running {
						info, err := n.client.ContainerInspect(ctx, c.ID)
						if err == nil && info.State != nil {
							if t, err := time.Parse(time.RFC3339Nano, info.State.StartedAt); err == nil {
								app.RunningSince = t
							}
						}
					}

					existing, found := appsByName[settings.Name]
					if !found || c.Created > existing.created {
						appsByName[settings.Name] = appCandidate{app: app, created: c.Created}
					}
				}
				break
			}
		}
	}

	for _, candidate := range appsByName {
		n.applications = append(n.applications, candidate.app)
	}

	n.sortApplications()
	return nil
}

func (n *Namespace) sortApplications() {
	slices.SortFunc(n.applications, func(a, b *Application) int {
		return strings.Compare(a.Settings.Host, b.Settings.Host)
	})
}

func (n *Namespace) parseBackup(r io.Reader) (ApplicationSettings, ApplicationVolumeSettings, []byte, error) {
	var appSettings ApplicationSettings
	var volSettings ApplicationVolumeSettings
	var volumeData bytes.Buffer

	gr, err := gzip.NewReader(r)
	if err != nil {
		return appSettings, volSettings, nil, fmt.Errorf("%w: %v", ErrInvalidBackup, err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	tw := tar.NewWriter(&volumeData)
	defer tw.Close()

	foundApp := false
	foundVol := false

	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return appSettings, volSettings, nil, fmt.Errorf("%w: %v", ErrInvalidBackup, err)
		}

		switch header.Name {
		case backupAppSettingsEntry:
			data, err := io.ReadAll(tr)
			if err != nil {
				return appSettings, volSettings, nil, fmt.Errorf("%w: reading application settings: %v", ErrInvalidBackup, err)
			}
			appSettings, err = UnmarshalApplicationSettings(string(data))
			if err != nil {
				return appSettings, volSettings, nil, fmt.Errorf("%w: parsing application settings: %v", ErrInvalidBackup, err)
			}
			foundApp = true

		case backupVolSettingsEntry:
			data, err := io.ReadAll(tr)
			if err != nil {
				return appSettings, volSettings, nil, fmt.Errorf("%w: reading volume settings: %v", ErrInvalidBackup, err)
			}
			volSettings, err = UnmarshalApplicationVolumeSettings(string(data))
			if err != nil {
				return appSettings, volSettings, nil, fmt.Errorf("%w: parsing volume settings: %v", ErrInvalidBackup, err)
			}
			foundVol = true

		default:
			if header.Name == BackupDataDir || strings.HasPrefix(header.Name, BackupDataDir+"/") {
				newHeader := *header
				if header.Name == BackupDataDir {
					newHeader.Name = "data"
				} else {
					newHeader.Name = "data" + strings.TrimPrefix(header.Name, BackupDataDir)
				}
				if err := tw.WriteHeader(&newHeader); err != nil {
					return appSettings, volSettings, nil, err
				}
				if header.Size > 0 {
					if _, err := io.Copy(tw, tr); err != nil {
						return appSettings, volSettings, nil, err
					}
				}
			}
		}
	}

	if !foundApp || !foundVol {
		return appSettings, volSettings, nil, fmt.Errorf("%w: missing required metadata files", ErrInvalidBackup)
	}

	return appSettings, volSettings, volumeData.Bytes(), nil
}

// Helpers

var validNamespace = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// withLoggingTransport wraps the client's transport after FromEnv has
// configured it, so every engine call gets a debug log line. The SDK's scheme
// detection and hijack dialing type-assert *http.Transport and silently lose
// TLS when the assertion fails, so a TLS-configured engine host (remote
// DOCKER_HOST) is left unwrapped rather than broken - verbose logging skips
// engine calls there.
func withLoggingTransport() client.Opt {
	return func(c *client.Client) error {
		httpClient := c.HTTPClient()
		transport, ok := httpClient.Transport.(*http.Transport)
		if !ok || transport.TLSClientConfig != nil {
			return nil
		}
		httpClient.Transport = logging.WrapTransport(transport)
		return client.WithHTTPClient(httpClient)(c)
	}
}
