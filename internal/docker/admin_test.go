package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestAdminCarriesTSDProxyLabels(t *testing.T) {
	labels := tsdproxyLabels("once-admin", true, false)

	assert.Equal(t, "true", labels["tsdproxy.enable"])
	assert.Equal(t, "once-admin", labels["tsdproxy.name"])
}
