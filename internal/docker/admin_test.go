package docker

import (
	"net"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminNginxConfigRoutesPort80ToSocket(t *testing.T) {
	conf := AdminNginxConfig()

	assert.Contains(t, conf, "listen 80;")
	assert.Contains(t, conf, "proxy_pass http://unix:/var/run/once-admin.sock:/;")
}

func TestAdminPathsHonorRuntimeDirOverride(t *testing.T) {
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", "/tmp/once-test")

	assert.Equal(t, "/tmp/once-test/once-admin.sock", AdminSocketPath())
	assert.Equal(t, "/tmp/once-test/once-admin-nginx.conf", AdminNginxConfPath())
}

func TestAdminPathsDefaultToVarRun(t *testing.T) {
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", "")

	assert.Equal(t, "/var/run/once-admin.sock", AdminSocketPath())
	assert.Equal(t, "/var/run/once-admin-nginx.conf", AdminNginxConfPath())
}

func TestRequireDaemonErrorsWhenSocketAbsent(t *testing.T) {
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", t.TempDir())

	err := NewAdmin(nil).RequireDaemon()

	require.Error(t, err)
	assert.Contains(t, err.Error(), AdminSocketPath())
	assert.Contains(t, err.Error(), "Admin socket server")
}

func TestRequireDaemonSucceedsWhenSocketAndConfigPresent(t *testing.T) {
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", t.TempDir())

	ln, err := net.Listen("unix", AdminSocketPath())
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	require.NoError(t, os.WriteFile(AdminNginxConfPath(), []byte(AdminNginxConfig()), 0o644))

	assert.NoError(t, NewAdmin(nil).RequireDaemon())
}

func TestRequireDaemonErrorsWhenConfigAbsent(t *testing.T) {
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", t.TempDir())

	ln, err := net.Listen("unix", AdminSocketPath())
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })

	err = NewAdmin(nil).RequireDaemon()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "nginx config")
}

func TestAdminCarriesTSDProxyLabels(t *testing.T) {
	labels := tsdproxyLabels("once-admin", true, false)

	assert.Equal(t, "true", labels["tsdproxy.enable"])
	assert.Equal(t, "once-admin", labels["tsdproxy.name"])
}
