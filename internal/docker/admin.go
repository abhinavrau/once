package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
)

// adminImage is pinned exactly, the same doctrine as proxyImage/docktailImage.
const adminImage = "nginx:1.27-alpine"

// once-admin bind-mounts the daemon's socket directory, never the socket file
// itself. /var/run is tmpfs, so after a reboot dockerd restarts once-admin
// before the daemon is up, and a missing bind source is auto-created as a
// directory. For the socket directory that is exactly right - the daemon then
// listens inside it and the container sees the socket appear. A file bind
// instead leaves a directory where the socket belongs, and also pins the
// socket's old inode across every daemon restart.
const (
	adminContainerSocketDir = "/run/once-admin"
	adminSocketName         = "admin.sock"
	adminNginxConfDir       = "/etc/nginx/conf.d"
)

// AdminSocketDir is the host directory the daemon serves its socket from and
// once-admin bind-mounts. The runtime dir is overridable so tests can use a
// writable, bind-mountable temp dir instead of /var/run.
func AdminSocketDir() string  { return filepath.Join(adminRuntimeDir(), "once-admin") }
func AdminSocketPath() string { return filepath.Join(AdminSocketDir(), adminSocketName) }

// Admin manages the once-admin nginx helper container, which Docktail exposes
// on the tailnet as a Tailscale Service and which reverse-proxies HTTP to the
// daemon's Unix socket.
type Admin struct {
	namespace *Namespace
}

func NewAdmin(ns *Namespace) *Admin {
	return &Admin{namespace: ns}
}

// RequireDaemon checks that the background daemon is serving the socket
// once-admin proxies to. It's the precondition for Boot, exposed so the enable
// lifecycle can fail fast before booting anything else.
func (a *Admin) RequireDaemon() error {
	if !isSocket(AdminSocketPath()) {
		return fmt.Errorf("once-admin socket %s not found; start the background daemon, or if it is running check its logs for an \"Admin socket server\" error", AdminSocketPath())
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
// host ports, bind-mounting the daemon's socket directory. The daemon must be
// running (the socket is the thing being exposed), so a missing socket is a
// clear error rather than a silently dead proxy. A once-admin with outdated
// mounts is recreated.
func (a *Admin) Boot(ctx context.Context) error {
	if err := a.RequireDaemon(); err != nil {
		return err
	}

	info, err := a.namespace.client.ContainerInspect(ctx, a.containerName())
	current := false
	if err == nil {
		if current, err = a.current(ctx, info); err != nil {
			return err
		}
	}
	if current {
		if err := a.ensureRunning(ctx, info); err != nil {
			return err
		}
		// Re-issue the route as well as start the container. once-admin's
		// route is otherwise only registered at create time, so it keeps
		// naming whatever tailnet host its node held then - and a node that
		// rejoins under a name Tailscale deduped (once-admin-1) leaves the
		// old route answering 404 with nothing to correct it. Boot runs on
		// Tailscale enable and recovery handoff, so the drift heals there.
		return a.registerTailnetRoute(ctx, info.ID, false)
	}
	if err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("inspecting once-admin container: %w", err)
	}
	return a.recreate(ctx, err == nil)
}

// Heal migrates a once-admin created by an older Once - which bind-mounted the
// socket and nginx config as individual files, or lacks a tailnet label the
// active topology now writes (ADR-0018) - to the current shape, and clears
// what those file binds left in the runtime dir. The daemon runs it at
// startup so an upgraded install heals without a Once command. An install
// without once-admin, or with a current one, is left alone.
func (a *Admin) Heal(ctx context.Context) error {
	info, err := a.namespace.client.ContainerInspect(ctx, a.containerName())
	switch {
	case errdefs.IsNotFound(err):
	case err != nil:
		return fmt.Errorf("inspecting once-admin container: %w", err)
	default:
		current, err := a.current(ctx, info)
		if err != nil {
			return err
		}
		if !current {
			if err := a.recreate(ctx, true); err != nil {
				return err
			}
		}
	}

	// Only once the legacy container is gone: until then a restart of it can
	// make dockerd recreate these.
	removeLegacyAdminPaths()
	return nil
}

// ReregisterRoute re-issues once-admin's Proxy route at its running
// container, for the post-proxy-roll sweep. A once-admin that is not part of
// this install has no route to re-issue and is not an error.
func (a *Admin) ReregisterRoute(ctx context.Context) error {
	info, err := a.namespace.client.ContainerInspect(ctx, a.containerName())
	if err != nil {
		if errdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("inspecting once-admin container: %w", err)
	}
	return a.registerTailnetRoute(ctx, info.ID, false)
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

// binds is deliberately a legacy Binds entry rather than a bind Mount, which
// would refuse to start once-admin ahead of the daemon after a reboot instead
// of auto-creating the socket directory.
func (a *Admin) binds() []string {
	return []string{AdminSocketDir() + ":" + adminContainerSocketDir + ":ro"}
}

func (a *Admin) mountsCurrent(info container.InspectResponse) bool {
	return info.HostConfig != nil && slices.Equal(info.HostConfig.Binds, a.binds())
}

// current reports whether once-admin needs no recreate: its mounts and its
// tailnet labels are what a create would give it now. A setup from before
// ADR-0015 cannot say which labels it wants until an enable rebuilds it, so
// its labels count as current.
func (a *Admin) current(ctx context.Context, info container.InspectResponse) (bool, error) {
	if !a.mountsCurrent(info) {
		return false, nil
	}
	want, err := a.tailnetLabels(ctx)
	if errors.Is(err, ErrOlderOnceSetup) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return carriesTailnetLabels(info.Config.Labels, want), nil
}

// tailnetLabels are once-admin's tailnet labels. once-admin is booted both by
// the Tailscale enable lifecycle (which exposes it on the tailnet too) and by
// the recovery handoff path (which never touches Tailscale), so they depend
// on whether Tailscale actually is enabled here, not on which caller booted it.
func (a *Admin) tailnetLabels(ctx context.Context) (map[string]string, error) {
	tailscaleEnabled, err := a.namespace.Tailscale().Enabled(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking tailscale: %w", err)
	}
	labels, err := a.namespace.Tailscale().AppLabels(ctx, tailscaleEnabled, "once-admin", a.namespace.name, 0)
	if err != nil {
		return nil, fmt.Errorf("resolving once-admin's tailnet labels: %w", err)
	}
	return labels, nil
}

func (a *Admin) ensureRunning(ctx context.Context, info container.InspectResponse) error {
	if !info.State.Running {
		if err := a.namespace.client.ContainerStart(ctx, info.ID, container.StartOptions{}); err != nil {
			return fmt.Errorf("starting once-admin container: %w", err)
		}
	}
	return nil
}

// recreate replaces once-admin. A replacement's tailnet node is the one the
// old container already held, so it is routed on whatever name that node has
// rather than refused as a fresh join Tailscale deduped.
func (a *Admin) recreate(ctx context.Context, replacing bool) error {
	if err := a.Destroy(ctx); err != nil {
		return err
	}
	if err := a.pullImage(ctx); err != nil {
		return err
	}
	return a.create(ctx, !replacing)
}

func (a *Admin) create(ctx context.Context, joining bool) error {
	labels, err := a.tailnetLabels(ctx)
	if err != nil {
		return err
	}

	resp, err := a.namespace.client.ContainerCreate(ctx,
		&container.Config{
			Image:  adminImage,
			Labels: labels,
		},
		&container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyAlways},
			LogConfig:     ContainerLogConfig(),
			Binds:         a.binds(),
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

	if err := a.writeNginxConfig(ctx, resp.ID); err != nil {
		a.namespace.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return err
	}

	if err := a.namespace.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		a.namespace.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return fmt.Errorf("starting once-admin container: %w", err)
	}

	if err := a.registerTailnetRoute(ctx, resp.ID, joining); err != nil {
		a.namespace.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return err
	}
	return nil
}

// writeNginxConfig copies the config into the created container's writable
// layer, where it survives restarts, so nginx never waits on a host file.
func (a *Admin) writeNginxConfig(ctx context.Context, containerID string) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	header := &tar.Header{Name: "default.conf", Mode: 0o644, Size: int64(len(adminNginxConf))}
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("writing tar header: %w", err)
	}
	if _, err := tw.Write([]byte(adminNginxConf)); err != nil {
		return fmt.Errorf("writing tar data: %w", err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("closing tar writer: %w", err)
	}
	if err := a.namespace.client.CopyToContainer(ctx, containerID, adminNginxConfDir, &buf, container.CopyToContainerOptions{}); err != nil {
		return fmt.Errorf("writing once-admin nginx config: %w", err)
	}
	return nil
}

// registerTailnetRoute gives once-admin the Proxy route it answers on under
// nodes, where it is exposed the same way an app is: TSDProxy forwards to the
// Proxy rather than the container directly. A no-op under services (Docktail
// advertises the container itself) or with Tailscale off. joining is true
// when the container was just created, so its node joins with this
// registration and gets the same deduped-name refusal an app's does.
func (a *Admin) registerTailnetRoute(ctx context.Context, containerID string, joining bool) error {
	enabled, err := a.namespace.Tailscale().Enabled(ctx)
	if err != nil {
		return fmt.Errorf("checking tailscale: %w", err)
	}
	if !enabled {
		return nil
	}
	viaProxy, err := a.namespace.Tailscale().RoutesTailnetAppsViaProxy(ctx)
	if err != nil || !viaProxy {
		return err
	}
	var joined string
	if !joining {
		if joined, err = a.namespace.Tailscale().NodeRouteHost(ctx, "once-admin"); err != nil {
			return fmt.Errorf("resolving once-admin's tailnet host: %w", err)
		}
	}
	if err := registerNodesProxyRoute(ctx, a.namespace, "once-admin", "once-admin", containerID, "", joined); err != nil {
		return fmt.Errorf("registering once-admin with the proxy: %w", err)
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

// removeLegacyAdminPaths clears the host paths older Onces bind-mounted as
// files: a stale socket or config, or the empty directories dockerd made of
// them after a reboot. os.Remove leaves anything non-empty alone.
func removeLegacyAdminPaths() {
	for _, name := range []string{"once-admin.sock", "once-admin-nginx.conf"} {
		_ = os.Remove(filepath.Join(adminRuntimeDir(), name))
	}
}

func isSocket(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

// adminNginxConf routes all HTTP on port 80 to the daemon's Unix socket. The
// unix: upstream path is the in-container mount, not the host path. nginx
// resolves it per request, so it starts fine before the socket exists.
const adminNginxConf = `server {
    listen 80;
    location / {
        proxy_pass http://unix:/run/once-admin/admin.sock:/;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }
}
`
