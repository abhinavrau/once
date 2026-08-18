package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
)

// adminImage is pinned exactly, the same doctrine as proxyImage/docktailImage.
const adminImage = "nginx:1.27-alpine"

// Paths inside the once-admin container. The host socket is bind-mounted to the
// fixed container path the nginx config proxies to.
const (
	adminContainerSocket = "/var/run/once-admin.sock"
	adminNginxConfTarget = "/etc/nginx/conf.d/default.conf"
)

// AdminSocketPath and AdminNginxConfPath are the host paths the daemon owns and
// the once-admin container bind-mounts. The runtime dir is overridable so tests
// can use a writable, bind-mountable temp dir instead of /var/run.
func AdminSocketPath() string    { return filepath.Join(adminRuntimeDir(), "once-admin.sock") }
func AdminNginxConfPath() string { return filepath.Join(adminRuntimeDir(), "once-admin-nginx.conf") }

// AdminNginxConfig is the nginx config routing HTTP port 80 to the Unix socket.
// The daemon writes it next to the socket; once-admin bind-mounts it read-only.
func AdminNginxConfig() string { return adminNginxConf }

// Admin manages the once-admin nginx helper container, which Docktail exposes
// on the tailnet as a Tailscale Service and which reverse-proxies HTTP to the
// daemon's Unix socket.
type Admin struct {
	namespace *Namespace
}

func NewAdmin(ns *Namespace) *Admin {
	return &Admin{namespace: ns}
}

// RequireDaemon checks that the background daemon has published the socket and
// nginx config once-admin bind-mounts. It's the precondition for Boot, exposed
// so the enable lifecycle can fail fast before booting anything else.
func (a *Admin) RequireDaemon() error {
	if !isSocket(AdminSocketPath()) {
		return fmt.Errorf("once-admin socket %s not found; start the background daemon, or if it is running check its logs for an \"Admin socket server\" error", AdminSocketPath())
	}
	if _, err := os.Stat(AdminNginxConfPath()); err != nil {
		return fmt.Errorf("once-admin nginx config %s not found; start the background daemon, or if it is running check its logs for an \"Admin socket server\" error", AdminNginxConfPath())
	}
	return nil
}

// Exists reports whether once-admin is already part of this install. Only the
// Tailscale lifecycle boots it, so a plain install has none - and anything that
// boots it for its own use has to know whether it is borrowing the container or
// creating one it should clean up.
func (a *Admin) Exists(ctx context.Context) bool {
	_, err := a.namespace.client.ContainerInspect(ctx, a.containerName())
	return err == nil
}

// Boot starts once-admin from the pinned nginx image on the once network with no
// host ports, bind-mounting the daemon socket and generated config. The daemon
// must be running (the socket is the thing being exposed), so a missing socket or
// config is a clear error rather than a silent broken mount.
func (a *Admin) Boot(ctx context.Context) error {
	if err := a.RequireDaemon(); err != nil {
		return err
	}
	socket, conf := AdminSocketPath(), AdminNginxConfPath()

	info, err := a.namespace.client.ContainerInspect(ctx, a.containerName())
	if err == nil {
		return a.ensureRunning(ctx, info)
	}
	if !errdefs.IsNotFound(err) {
		return fmt.Errorf("inspecting once-admin container: %w", err)
	}

	if err := a.pullImage(ctx); err != nil {
		return err
	}
	return a.create(ctx, socket, conf)
}

// Destroy stops and removes once-admin.
func (a *Admin) Destroy(ctx context.Context) error {
	if err := a.namespace.client.ContainerRemove(ctx, a.containerName(), container.RemoveOptions{Force: true}); err != nil {
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("removing once-admin: %w", err)
		}
	}
	return nil
}

// Private

func (a *Admin) containerName() string {
	return a.namespace.name + "-admin"
}

func (a *Admin) ensureRunning(ctx context.Context, info container.InspectResponse) error {
	if !info.State.Running {
		if err := a.namespace.client.ContainerStart(ctx, info.ID, container.StartOptions{}); err != nil {
			return fmt.Errorf("starting once-admin container: %w", err)
		}
	}
	return nil
}

func (a *Admin) create(ctx context.Context, hostSocket, hostConf string) error {
	// once-admin is booted both by the Tailscale enable lifecycle (which
	// exposes it as a tailnet service too) and by the recovery handoff path
	// (which never touches Tailscale), so its tailnet labels depend on
	// whether Tailscale actually is enabled here, not on which caller booted
	// it.
	tailscaleEnabled, err := a.namespace.Tailscale().Enabled(ctx)
	if err != nil {
		return fmt.Errorf("checking tailscale: %w", err)
	}
	labels, err := a.namespace.Tailscale().AppLabels(ctx, tailscaleEnabled, "once-admin", a.namespace.name, 0)
	if err != nil {
		return fmt.Errorf("resolving once-admin's tailnet labels: %w", err)
	}

	resp, err := a.namespace.client.ContainerCreate(ctx,
		&container.Config{
			Image:  adminImage,
			Labels: labels,
		},
		&container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyAlways},
			LogConfig:     ContainerLogConfig(),
			Binds: []string{
				hostSocket + ":" + adminContainerSocket,
				hostConf + ":" + adminNginxConfTarget + ":ro",
			},
		},
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				a.namespace.name: {},
			},
		},
		nil,
		a.containerName(),
	)
	if err != nil {
		return fmt.Errorf("creating once-admin container: %w", err)
	}

	if err := a.namespace.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		a.namespace.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return fmt.Errorf("starting once-admin container: %w", err)
	}

	// Under nodes, once-admin is exposed the same way an app is: TSDProxy
	// forwards it to the Proxy rather than the container directly, so it
	// needs a Proxy route on its own tailnet Host too.
	if tailscaleEnabled {
		settings, err := a.namespace.Tailscale().LoadSettings(ctx)
		if err == nil && topologyRoutesTailnetAppsViaProxy(settings.Topology) {
			if err := registerNodesProxyRoute(ctx, a.namespace, "once-admin", "once-admin", resp.ID, ""); err != nil {
				a.namespace.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
				return fmt.Errorf("registering once-admin with the proxy: %w", err)
			}
		}
	}
	return nil
}

func (a *Admin) pullImage(ctx context.Context) error {
	reader, err := a.namespace.client.ImagePull(ctx, adminImage, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pulling once-admin image: %w", err)
	}
	defer reader.Close()
	_, _ = io.Copy(io.Discard, reader)
	return nil
}

// Helpers

func adminRuntimeDir() string {
	if d := os.Getenv("ONCE_ADMIN_RUNTIME_DIR"); d != "" {
		return d
	}
	return "/var/run"
}

func isSocket(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

// adminNginxConf routes all HTTP on port 80 to the daemon's Unix socket. The
// unix: upstream path is the in-container mount target, not the host path.
const adminNginxConf = `server {
    listen 80;
    location / {
        proxy_pass http://unix:/var/run/once-admin.sock:/;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }
}
`
