package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
)

// TestOnceAdminRecoversWhenTheDaemonComesUpAfterIt replays a reboot: dockerd
// restarts once-admin while the daemon, and so its socket, is gone. once-admin
// has to come up anyway and start serving the moment the daemon is back,
// including on a socket inode it has never seen.
func TestOnceAdminRecoversWhenTheDaemonComesUpAfterIt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ns, cli := newAdminTestNamespace(t, ctx)
	adminName := ns.Name() + "-admin"

	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", t.TempDir())
	stopDaemon := runAdminServer(t, ctx)
	require.NoError(t, ns.Admin().Boot(ctx))
	assertOnceAdminServesTheDaemon(t, ctx, cli, adminName)

	stopDaemon()
	require.NoFileExists(t, docker.AdminSocketPath())
	require.NoError(t, cli.ContainerRestart(ctx, adminName, container.StopOptions{}))
	info, err := cli.ContainerInspect(ctx, adminName)
	require.NoError(t, err)
	require.True(t, info.State.Running, "once-admin must start without the daemon's socket")

	stopDaemon = runAdminServer(t, ctx)
	assertOnceAdminServesTheDaemon(t, ctx, cli, adminName)

	// A real reboot wipes the socket directory too. dockerd recreates it
	// (root-owned, so this unprivileged suite can't then serve in it, as the
	// root daemon does) and once-admin still starts.
	stopDaemon()
	require.NoError(t, os.Remove(docker.AdminSocketDir()))
	require.NoError(t, cli.ContainerRestart(ctx, adminName, container.StopOptions{}))
	info, err = cli.ContainerInspect(ctx, adminName)
	require.NoError(t, err)
	assert.True(t, info.State.Running, "once-admin must start without the daemon's socket directory")
	assert.DirExists(t, docker.AdminSocketDir())
}

// TestOnceAdminHealMigratesLegacyFileBinds covers an install whose once-admin
// was created by an older Once, bind-mounting the socket and nginx config as
// files, on a host where a reboot has already turned both into directories.
func TestOnceAdminHealMigratesLegacyFileBinds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ns, cli := newAdminTestNamespace(t, ctx)
	adminName := ns.Name() + "-admin"
	startAdminServer(t, ctx)

	runtimeDir := os.Getenv("ONCE_ADMIN_RUNTIME_DIR")
	legacySocket := filepath.Join(runtimeDir, "once-admin.sock")
	legacyConf := filepath.Join(runtimeDir, "once-admin-nginx.conf")
	require.NoError(t, os.Mkdir(legacySocket, 0o755))
	require.NoError(t, os.Mkdir(legacyConf, 0o755))

	pullImage(t, ctx, cli, "nginx:1.27-alpine")
	_, err := cli.ContainerCreate(ctx,
		&container.Config{Image: "nginx:1.27-alpine"},
		&container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyAlways},
			Binds: []string{
				legacySocket + ":/var/run/once-admin.sock",
				legacyConf + ":/etc/nginx/conf.d/default.conf:ro",
			},
		},
		nil, nil, adminName)
	require.NoError(t, err)

	require.NoError(t, ns.Admin().Heal(ctx))

	assert.NoDirExists(t, legacySocket)
	assert.NoDirExists(t, legacyConf)
	info, err := cli.ContainerInspect(ctx, adminName)
	require.NoError(t, err)
	assert.Equal(t, []string{docker.AdminSocketDir() + ":/run/once-admin:ro"}, info.HostConfig.Binds)
	assertOnceAdminServesTheDaemon(t, ctx, cli, adminName)

	// Healing a current once-admin leaves it be.
	require.NoError(t, ns.Admin().Heal(ctx))
	after, err := cli.ContainerInspect(ctx, adminName)
	require.NoError(t, err)
	assert.Equal(t, info.ID, after.ID)
}

// Helpers

func newAdminTestNamespace(t *testing.T, ctx context.Context) (*docker.Namespace, *client.Client) {
	t.Helper()
	ns, err := docker.NewNamespace("once-admin-test")
	require.NoError(t, err)
	t.Cleanup(func() { ns.Teardown(context.Background(), true) })
	require.NoError(t, ns.EnsureNetwork(ctx))

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	t.Cleanup(func() { cli.Close() })
	return ns, cli
}

// assertOnceAdminServesTheDaemon requests the daemon's health endpoint through
// once-admin's nginx, from inside the container.
func assertOnceAdminServesTheDaemon(t *testing.T, ctx context.Context, cli *client.Client, adminName string) {
	t.Helper()
	assert.Eventually(t, func() bool {
		out, _ := execCapture(ctx, cli, adminName, "wget", "-qO-", "http://127.0.0.1/up")
		return out == "once-admin ok\n"
	}, 30*time.Second, 500*time.Millisecond, "once-admin never proxied to the daemon")
}
