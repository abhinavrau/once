package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTailscaleSettingsRoundTrip(t *testing.T) {
	settings := TailscaleSettings{ClientID: "id-123", ClientSecret: "secret-456"}

	parsed, err := UnmarshalTailscaleSettings(settings.Marshal())
	require.NoError(t, err)
	assert.Equal(t, settings, parsed)
}

func TestBuildTSDProxyConfigOAuth(t *testing.T) {
	config := buildTSDProxyConfig(TailscaleSettings{ClientID: "id-123", ClientSecret: "secret-456"}, "", "")

	assert.Contains(t, config, `clientId: "id-123"`)
	assert.Contains(t, config, `clientSecret: "secret-456"`)
	assert.NotContains(t, config, "controlUrl")
	assert.NotContains(t, config, "authKey")
	assert.Contains(t, config, "dataDir: /data/")
	assert.Contains(t, config, "unix:///var/run/docker.sock")
}

func TestBuildTSDProxyConfigControlSeam(t *testing.T) {
	// When the control seam is set, authKey + controlUrl replace OAuth entirely.
	config := buildTSDProxyConfig(
		TailscaleSettings{ClientID: "id-123", ClientSecret: "secret-456"},
		"http://headscale:8080", "tskey-auth-abc",
	)

	assert.Contains(t, config, `authKey: "tskey-auth-abc"`)
	assert.Contains(t, config, "controlUrl: http://headscale:8080")
	assert.NotContains(t, config, "clientId")
	assert.NotContains(t, config, "clientSecret")
}
