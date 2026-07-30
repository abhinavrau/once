package docker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const tsDevicesPath = "/tailnet/-/devices"

func TestHostPreflightRequiresHostSocket(t *testing.T) {
	t.Setenv("ONCE_TAILSCALED_DIR", t.TempDir())

	// The daemon fake errors if consulted: the socket check must come first.
	err := preflightAgainst(newFakeTailscaleAPI(t), &FakeTailscaleDaemon{StatusErr: errors.New("daemon must not be consulted")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), tailscaledSocketFile)
	assert.Contains(t, err.Error(), "--mode sidecar")
}

func TestHostPreflightRequiresNodeTag(t *testing.T) {
	plantHostSocket(t)

	err := preflightAgainst(newFakeTailscaleAPI(t), fakeHostDaemon("1.98.9-t1234abcd"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sudo tailscale up --advertise-tags=tag:once")
}

func TestHostPreflightRequiresServicesCapableVersion(t *testing.T) {
	plantHostSocket(t)

	err := preflightAgainst(newFakeTailscaleAPI(t), fakeHostDaemon("1.84.2-t1234abcd", "tag:once"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1.84.2")
	assert.Contains(t, err.Error(), "v1.92 or newer")
}

func TestHostPreflightRejectsNodeOutsideTailnet(t *testing.T) {
	plantHostSocket(t)
	api := devicesAPI(t, "nodekey:someone-else")

	err := preflightAgainst(api, fakeHostDaemon("1.98.9-t1234abcd", "tag:once"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "different tailnet")
	assert.Contains(t, err.Error(), "sudo tailscale login")

	// The membership read must stay byte-compatible with the devices list
	// Docktail's tailnet reads use: GET /tailnet/-/devices.
	assert.Equal(t, []string{tsDevicesPath}, api.paths(http.MethodGet))
}

func TestHostPreflightPasses(t *testing.T) {
	plantHostSocket(t)

	require.NoError(t, preflightAgainst(devicesAPI(t, "nodekey:someone-else", "nodekey:host"),
		fakeHostDaemon("1.98.9-t1234abcd", "tag:once")))
}

func TestHostPreflightControlSeamSkipsTailnetCheck(t *testing.T) {
	plantHostSocket(t)
	t.Setenv(envControlURL, "http://headscale:8080")
	t.Setenv(envAuthKey, "tskey-auth-abc")

	// The cloud check must not run on the seam: the seam cans nothing, so any
	// call would fail the test...
	require.NoError(t, preflightAgainst(newFakeTailscaleAPI(t), fakeHostDaemon("1.98.9-t1234abcd", "tag:once")))

	// ...but the local checks still apply there.
	err := preflightAgainst(newFakeTailscaleAPI(t), fakeHostDaemon("1.84.0-t1234abcd", "tag:once"))
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

// Helpers

// devicesAPI cans the tailnet's devices list, so the membership check parses
// the same JSON shape the real API returns.
func devicesAPI(t *testing.T, nodeKeys ...string) *fakeTailscaleAPI {
	t.Helper()
	devices := []map[string]string{}
	for _, key := range nodeKeys {
		devices = append(devices, map[string]string{"nodeKey": key})
	}
	body, err := json.Marshal(map[string]any{"devices": devices})
	require.NoError(t, err)

	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodGet, tsDevicesPath, http.StatusOK, string(body))
	return api
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

func preflightAgainst(api tailscaleAPI, daemon TailscaleDaemon) error {
	return validateHostModePreflight(context.Background(), api, daemon,
		TailscaleSettings{ClientID: "id", ClientSecret: "secret", Tag: "tag:once", Mode: TailscaleModeHost})
}
