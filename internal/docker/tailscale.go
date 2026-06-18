package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
)

// tsdproxyImage is pinned to an exact version, the same doctrine as proxyImage.
// Bumped via Once releases through the self-update mechanism.
const tsdproxyImage = "almeidapaulopt/tsdproxy:2.3.4"

const tsdproxyConfigName = "tsdproxy.yaml"

// Hidden control-server seam: when these env vars are set, the tsdproxy is
// configured with controlUrl + authKey instead of OAuth. No CLI/TUI exposes
// them — they exist to point once-tsdproxy at the headscale test harness, and
// are promotable to first-class headscale support later.
const (
	envControlURL = "ONCE_TAILSCALE_CONTROL_URL"
	envAuthKey    = "ONCE_TAILSCALE_AUTH_KEY"
)

type TailscaleSettings struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
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

// Tailscale manages the once-tsdproxy helper container, mirroring Proxy. The
// container's existence is the source of truth for "Tailscale enabled"; its
// settings live in the once label on the container itself.
type Tailscale struct {
	namespace *Namespace
	Settings  *TailscaleSettings
}

func NewTailscale(ns *Namespace) *Tailscale {
	return &Tailscale{namespace: ns}
}

// Enable boots once-tsdproxy from the pinned image, mounting the Docker socket
// and the once-tsdproxy-data volume in userspace networking mode. Re-running
// with new credentials recreates only this container; the data volume (node
// identities, Magic DNS names) is left untouched.
func (t *Tailscale) Enable(ctx context.Context, settings TailscaleSettings) error {
	info, err := t.namespace.client.ContainerInspect(ctx, t.containerName())
	if err == nil {
		if info.Config.Labels[labelKey] == settings.Marshal() {
			return t.ensureRunning(ctx, info)
		}
		// Credentials changed: recreate the container, keep the data volume.
		if err := t.removeContainer(ctx); err != nil {
			return err
		}
	} else if !errdefs.IsNotFound(err) {
		return fmt.Errorf("inspecting tsdproxy container: %w", err)
	}

	if err := t.pullImage(ctx); err != nil {
		return err
	}

	if err := t.create(ctx, settings); err != nil {
		return err
	}

	t.Settings = &settings

	// Retrofit running apps so they appear on the tailnet immediately, not just
	// on their next deploy. once-tsdproxy now exists, so the roll injects labels.
	return t.namespace.RollApplications(ctx)
}

// Disable stops and removes once-tsdproxy but keeps the once-tsdproxy-data
// volume, so node identities survive a later re-enable without suffix churn.
func (t *Tailscale) Disable(ctx context.Context) error {
	if err := t.removeContainer(ctx); err != nil {
		return err
	}
	t.Settings = nil

	// Roll running apps to strip the tsdproxy.* labels — once-tsdproxy is gone,
	// so Enabled() is now false and the roll recreates them without the labels.
	return t.namespace.RollApplications(ctx)
}

// Enabled is implicit in the existence of the once-tsdproxy container (the same
// doctrine the proxy follows).
func (t *Tailscale) Enabled(ctx context.Context) (bool, error) {
	_, err := t.namespace.client.ContainerInspect(ctx, t.containerName())
	if err == nil {
		return true, nil
	}
	if errdefs.IsNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("inspecting tsdproxy container: %w", err)
}

// Destroy removes both the container and the data volume (full cleanup, used by
// teardown — unlike Disable, which retains the volume).
func (t *Tailscale) Destroy(ctx context.Context) error {
	if err := t.removeContainer(ctx); err != nil {
		return err
	}
	if err := t.namespace.client.VolumeRemove(ctx, t.dataVolumeName(), true); err != nil {
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("removing tsdproxy volume: %w", err)
		}
	}
	t.Settings = nil
	return nil
}

// Private

func (t *Tailscale) containerName() string {
	return t.namespace.name + "-tsdproxy"
}

func (t *Tailscale) dataVolumeName() string {
	return t.namespace.name + "-tsdproxy-data"
}

func (t *Tailscale) ensureRunning(ctx context.Context, info container.InspectResponse) error {
	if !info.State.Running {
		if err := t.namespace.client.ContainerStart(ctx, info.ID, container.StartOptions{}); err != nil {
			return fmt.Errorf("starting tsdproxy container: %w", err)
		}
	}
	if label := info.Config.Labels[labelKey]; label != "" {
		settings, err := UnmarshalTailscaleSettings(label)
		if err != nil {
			return fmt.Errorf("unmarshalling tsdproxy settings: %w", err)
		}
		t.Settings = &settings
	}
	return nil
}

func (t *Tailscale) create(ctx context.Context, settings TailscaleSettings) error {
	name := t.containerName()

	resp, err := t.namespace.client.ContainerCreate(ctx,
		&container.Config{
			Image: tsdproxyImage,
			Labels: map[string]string{
				labelKey: settings.Marshal(),
			},
		},
		&container.HostConfig{
			// ponytail: no /dev/net/tun, no NET_ADMIN — tsnet runs userspace natively.
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyAlways},
			LogConfig:     ContainerLogConfig(),
			Binds:         []string{"/var/run/docker.sock:/var/run/docker.sock"},
			Mounts: []mount.Mount{
				{Type: mount.TypeVolume, Source: t.dataVolumeName(), Target: "/data"},
			},
		},
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				t.namespace.name: {},
			},
		},
		nil,
		name,
	)
	if err != nil {
		return fmt.Errorf("creating tsdproxy container: %w", err)
	}

	config := buildTSDProxyConfig(settings, os.Getenv(envControlURL), os.Getenv(envAuthKey))
	if err := t.copyConfig(ctx, resp.ID, config); err != nil {
		t.namespace.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return err
	}

	if err := t.namespace.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		t.namespace.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return fmt.Errorf("starting tsdproxy container: %w", err)
	}

	return nil
}

// copyConfig writes the tsdproxy config into /config before the container
// starts, mirroring how the proxy persists state via CopyToContainer. The tar
// carries the /config directory entry too, so it need not pre-exist.
func (t *Tailscale) copyConfig(ctx context.Context, containerID, config string) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	dir := "config/"
	if err := tw.WriteHeader(&tar.Header{Name: dir, Mode: 0o755, Typeflag: tar.TypeDir}); err != nil {
		return fmt.Errorf("writing config dir header: %w", err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: dir + tsdproxyConfigName, Mode: 0o644, Size: int64(len(config))}); err != nil {
		return fmt.Errorf("writing config header: %w", err)
	}
	if _, err := tw.Write([]byte(config)); err != nil {
		return fmt.Errorf("writing config data: %w", err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("closing config tar: %w", err)
	}

	if err := t.namespace.client.CopyToContainer(ctx, containerID, "/", &buf, container.CopyToContainerOptions{}); err != nil {
		return fmt.Errorf("copying tsdproxy config: %w", err)
	}
	return nil
}

func (t *Tailscale) pullImage(ctx context.Context) error {
	reader, err := t.namespace.client.ImagePull(ctx, tsdproxyImage, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pulling tsdproxy image: %w", err)
	}
	defer reader.Close()
	_, _ = io.Copy(io.Discard, reader)
	return nil
}

func (t *Tailscale) removeContainer(ctx context.Context) error {
	if err := t.namespace.client.ContainerRemove(ctx, t.containerName(), container.RemoveOptions{Force: true}); err != nil {
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("removing tsdproxy: %w", err)
		}
	}
	return nil
}

// Helpers

// tsdproxyLabels returns the labels that expose an app on the tailnet under its
// own Magic DNS name. Nil when Tailscale is disabled, so app containers carry
// the labels only while once-tsdproxy is running. The 80/http upstream matches
// Once's port-80 assumption; ephemeral nodes self-clean when the app is deleted.
func tsdproxyLabels(appName string, enabled bool) map[string]string {
	if !enabled {
		return nil
	}
	return map[string]string{
		"tsdproxy.enable":    "true",
		"tsdproxy.name":      appName,
		"tsdproxy.port.1":    "80/http:80/http",
		"tsdproxy.ephemeral": "true",
	}
}

// buildTSDProxyConfig renders the tsdproxy YAML config. When controlURL and
// authKey are set (the hidden control seam), they replace OAuth entirely —
// OAuth is Tailscale-SaaS-only and cannot reach a headscale control plane.
func buildTSDProxyConfig(settings TailscaleSettings, controlURL, authKey string) string {
	var provider string
	if controlURL != "" && authKey != "" {
		provider = fmt.Sprintf("      authKey: \"%s\"\n      authKeyFile: \"\"\n      controlUrl: %s\n", authKey, controlURL)
	} else {
		provider = fmt.Sprintf("      clientId: \"%s\"\n      clientSecret: \"%s\"\n", settings.ClientID, settings.ClientSecret)
	}
	return fmt.Sprintf(tsdproxyConfigTmpl, provider)
}

// tsdproxyConfigTmpl matches the config proven against tsdproxy:2 in the
// integration harness. %s is the provider auth block (indented 6 spaces).
const tsdproxyConfigTmpl = `defaultProxyProvider: default
docker:
  local:
    host: unix:///var/run/docker.sock
    targetHostname: host.docker.internal
    tryDockerInternalNetwork: true
    defaultProxyProvider: default
tailscale:
  providers:
    default:
%s  dataDir: /data/
http:
  hostname: 0.0.0.0
  port: 8080
log:
  level: info
  json: false
`
