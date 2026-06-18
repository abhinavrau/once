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

func TestTSDProxyLabelsWhenEnabled(t *testing.T) {
	labels := tsdproxyLabels("writebook", true)

	assert.Equal(t, map[string]string{
		"tsdproxy.enable":    "true",
		"tsdproxy.name":      "writebook",
		"tsdproxy.port.1":    "80/http:80/http",
		"tsdproxy.ephemeral": "true",
	}, labels)
}

func TestTSDProxyLabelsWhenDisabled(t *testing.T) {
	assert.Nil(t, tsdproxyLabels("writebook", false))
}

func TestContainerConfigInjectsTSDProxyLabels(t *testing.T) {
	app := &Application{Settings: ApplicationSettings{Name: "writebook", Image: "writebook:1"}}

	enabled := app.containerConfig(nil, true)
	assert.Equal(t, app.Settings.Marshal(), enabled.Labels[labelKey])
	assert.Equal(t, "true", enabled.Labels["tsdproxy.enable"])
	assert.Equal(t, "writebook", enabled.Labels["tsdproxy.name"])

	disabled := app.containerConfig(nil, false)
	assert.Equal(t, app.Settings.Marshal(), disabled.Labels[labelKey])
	assert.NotContains(t, disabled.Labels, "tsdproxy.enable")
}

func TestBuildTSDProxyConfigOAuth(t *testing.T) {
	config := buildTSDProxyConfig(TailscaleSettings{ClientID: "id-123", ClientSecret: "secret-456"}, "", "")

	assert.Contains(t, config, `clientId: "id-123"`)
	assert.Contains(t, config, `clientSecret: "secret-456"`)
	assert.NotContains(t, config, "controlUrl")
	assert.NotContains(t, config, "authKey")
	assert.Contains(t, config, "dataDir: /data/")
	assert.Contains(t, config, "unix:///var/run/docker.sock")
	// Apps share the once network with tsdproxy, so it must reach them by their
	// container IP rather than falling back to host.docker.internal.
	assert.Contains(t, config, "tryDockerInternalNetwork: true")
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
