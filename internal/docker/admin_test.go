package docker

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminNginxConfigRoutesPort80ToSocket(t *testing.T) {
	assert.Contains(t, adminNginxConf, "listen 80;")
	assert.Contains(t, adminNginxConf, "proxy_pass http://unix:/run/once-admin/admin.sock:/;")
}

func TestAdminPathsHonorRuntimeDirOverride(t *testing.T) {
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", "/tmp/once-test")

	assert.Equal(t, "/tmp/once-test/once-admin", AdminSocketDir())
	assert.Equal(t, "/tmp/once-test/once-admin/admin.sock", AdminSocketPath())
}

func TestAdminPathsDefaultToVarRun(t *testing.T) {
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", "")

	assert.Equal(t, "/var/run/once-admin", AdminSocketDir())
	assert.Equal(t, "/var/run/once-admin/admin.sock", AdminSocketPath())
}

func TestRequireDaemonErrorsWhenSocketAbsent(t *testing.T) {
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", t.TempDir())

	err := NewAdmin(nil).RequireDaemon()

	require.Error(t, err)
	assert.Contains(t, err.Error(), AdminSocketPath())
	assert.Contains(t, err.Error(), "Admin socket server")
}

func TestRequireDaemonSucceedsWhenSocketPresent(t *testing.T) {
	listenOnAdminSocket(t)

	assert.NoError(t, NewAdmin(nil).RequireDaemon())
}

func TestAdminMountsTheSocketDirectoryNotFiles(t *testing.T) {
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", "/var/run")
	admin := NewAdmin(nil)

	inspected := func(binds ...string) container.InspectResponse {
		return container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{
			HostConfig: &container.HostConfig{Binds: binds},
		}}
	}
	current := inspected("/var/run/once-admin:/run/once-admin:ro")
	legacy := inspected(
		"/var/run/once-admin.sock:/var/run/once-admin.sock",
		"/var/run/once-admin-nginx.conf:/etc/nginx/conf.d/default.conf:ro",
	)

	assert.Equal(t, []string{"/var/run/once-admin:/run/once-admin:ro"}, admin.binds())
	assert.True(t, admin.mountsCurrent(current))
	assert.False(t, admin.mountsCurrent(legacy))
}

func TestRemoveLegacyAdminPathsClearsRebootDebris(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", dir)

	// What dockerd leaves when it restarts a legacy once-admin after a reboot
	// wiped /var/run: both bind sources auto-created as empty directories.
	require.NoError(t, os.Mkdir(filepath.Join(dir, "once-admin.sock"), 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "once-admin-nginx.conf"), 0o755))

	removeLegacyAdminPaths()

	assert.NoDirExists(t, filepath.Join(dir, "once-admin.sock"))
	assert.NoDirExists(t, filepath.Join(dir, "once-admin-nginx.conf"))
}

func TestAdminCarriesDocktailLabels(t *testing.T) {
	labels := (&servicesTopology{}).appLabels("once-admin", "once", 0)

	assert.Equal(t, "true", labels["docktail.service.enable"])
	assert.Equal(t, "once-admin", labels["docktail.service.name"])
	assert.Equal(t, "once", labels["docktail.service.network"])
}

// Helpers

// listenOnAdminSocket stands in for the daemon: a temp runtime dir with a live
// socket where once-admin expects it.
func listenOnAdminSocket(t *testing.T) {
	t.Helper()
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", t.TempDir())
	require.NoError(t, os.MkdirAll(AdminSocketDir(), 0o755))
	ln, err := net.Listen("unix", AdminSocketPath())
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
}
