package docker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// devicesServer stands in for the Tailscale API's token and devices-list
// endpoints, recording the devices request so tests can assert its shape.
type devicesServer struct {
	srv         *httptest.Server
	nodeKeys    []string
	devicesPath string
	devicesAuth string
}

func newDevicesServer(t *testing.T, nodeKeys ...string) *devicesServer {
	t.Helper()
	d := &devicesServer{nodeKeys: nodeKeys}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "tskey-api-probe"})
		case r.Method == http.MethodGet && r.URL.Path == "/tailnet/-/devices":
			d.devicesPath = r.URL.RequestURI()
			d.devicesAuth = r.Header.Get("Authorization")
			devices := []map[string]string{}
			for _, key := range d.nodeKeys {
				devices = append(devices, map[string]string{"nodeKey": key})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"devices": devices})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func plantHostSocket(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, tailscaledSocketFile), nil, 0o600))
	t.Setenv("ONCE_TAILSCALED_DIR", dir)
}

func fakeHostDaemon(version string, tags ...string) *FakeTailscaleDaemon {
	self := map[string]any{"PublicKey": "nodekey:host"}
	if len(tags) > 0 {
		self["Tags"] = tags
	}
	status, _ := json.Marshal(map[string]any{"Version": version, "Self": self})
	return &FakeTailscaleDaemon{StatusJSON: string(status)}
}

func preflightAgainst(baseURL string, daemon TailscaleDaemon) error {
	return validateHostModePreflight(context.Background(), baseURL, daemon,
		TailscaleSettings{ClientID: "id", ClientSecret: "secret", Tag: "tag:once", Mode: TailscaleModeHost})
}

func TestHostPreflightRequiresHostSocket(t *testing.T) {
	t.Setenv("ONCE_TAILSCALED_DIR", t.TempDir())

	// The daemon fake errors if consulted: the socket check must come first.
	err := preflightAgainst("http://unused.invalid", &FakeTailscaleDaemon{StatusErr: errors.New("daemon must not be consulted")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), tailscaledSocketFile)
	assert.Contains(t, err.Error(), "--mode sidecar")
}

func TestHostPreflightRequiresNodeTag(t *testing.T) {
	plantHostSocket(t)

	err := preflightAgainst("http://unused.invalid", fakeHostDaemon("1.98.9-t1234abcd"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sudo tailscale up --advertise-tags=tag:once")
}

func TestHostPreflightRequiresServicesCapableVersion(t *testing.T) {
	plantHostSocket(t)

	err := preflightAgainst("http://unused.invalid", fakeHostDaemon("1.84.2-t1234abcd", "tag:once"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1.84.2")
	assert.Contains(t, err.Error(), "v1.92 or newer")
}

func TestHostPreflightRejectsNodeOutsideTailnet(t *testing.T) {
	plantHostSocket(t)
	srv := newDevicesServer(t, "nodekey:someone-else")

	err := preflightAgainst(srv.srv.URL, fakeHostDaemon("1.98.9-t1234abcd", "tag:once"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "different tailnet")
	assert.Contains(t, err.Error(), "sudo tailscale login")

	assert.Equal(t, "/tailnet/-/devices", srv.devicesPath)
	assert.Equal(t, "Bearer tskey-api-probe", srv.devicesAuth)
}

func TestHostPreflightPasses(t *testing.T) {
	plantHostSocket(t)
	srv := newDevicesServer(t, "nodekey:someone-else", "nodekey:host")

	require.NoError(t, preflightAgainst(srv.srv.URL, fakeHostDaemon("1.98.9-t1234abcd", "tag:once")))
}

func TestHostPreflightControlSeamSkipsTailnetCheck(t *testing.T) {
	plantHostSocket(t)
	t.Setenv(envControlURL, "http://headscale:8080")
	t.Setenv(envAuthKey, "tskey-auth-abc")

	// The cloud check must not run on the seam (the base URL is unreachable,
	// so any attempt would fail)...
	require.NoError(t, preflightAgainst("http://unreachable.invalid", fakeHostDaemon("1.98.9-t1234abcd", "tag:once")))

	// ...but the local checks still apply there.
	err := preflightAgainst("http://unreachable.invalid", fakeHostDaemon("1.84.0-t1234abcd", "tag:once"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "v1.92 or newer")
}

func TestHostDaemonSupportsServices(t *testing.T) {
	assert.True(t, hostDaemonSupportsServices("1.92.1-t1234abcd"))
	assert.True(t, hostDaemonSupportsServices("1.98.9"))
	assert.True(t, hostDaemonSupportsServices("2.0.1"))
	// Services exist from 1.86, but docktail proxies to container IPs, which
	// serve only allows from 1.92.1 (remote service targets).
	assert.False(t, hostDaemonSupportsServices("1.88.4-t1234abcd"))
	assert.False(t, hostDaemonSupportsServices("1.84.2-t1234abcd"))
	assert.False(t, hostDaemonSupportsServices(""))
	assert.False(t, hostDaemonSupportsServices("unknown"))
}
