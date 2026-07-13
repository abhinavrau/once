package docker

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTailscaleSettingsRoundTrip(t *testing.T) {
	settings := TailscaleSettings{
		ClientID:     "id-123",
		ClientSecret: "secret-456",
		Tag:          "tag:once",
		DomainSuffix: "tailnet-name.ts.net",
		Mode:         TailscaleModeSidecar,
	}

	parsed, err := UnmarshalTailscaleSettings(settings.Marshal())
	require.NoError(t, err)
	assert.Equal(t, settings, parsed)
}

func TestTailscaleSettingsDropLegacyAPIKey(t *testing.T) {
	// Settings persisted by a TSDProxy-era install carry an apiKey field; it is
	// ignored rather than failing the unmarshal.
	parsed, err := UnmarshalTailscaleSettings(`{"clientId":"id","clientSecret":"secret","apiKey":"key-789"}`)
	require.NoError(t, err)
	assert.Equal(t, TailscaleSettings{ClientID: "id", ClientSecret: "secret"}, parsed)
	assert.NotContains(t, parsed.Marshal(), "apiKey")
}

func TestNormalizeTag(t *testing.T) {
	normalize := func(tag string) string {
		return TailscaleSettings{Tag: tag}.normalizeTag()
	}
	assert.Equal(t, "tag:once", normalize("once"))
	assert.Equal(t, "tag:once", normalize("tag:once"))
	assert.Equal(t, "tag:once,tag:admin", normalize("once,tag:admin"))
	assert.Equal(t, "tag:once,tag:admin", normalize(" once , tag:admin "))
	// Empty or whitespace-only collapses to "" so the "tag is required" check fires.
	assert.Empty(t, normalize(""))
	assert.Empty(t, normalize("  "))
}

func TestTailnetURL(t *testing.T) {
	assert.Equal(t, "https://books.tailnet-name.ts.net",
		TailscaleSettings{DomainSuffix: "tailnet-name.ts.net"}.TailnetURL("books"))
	// No suffix known yet -> no URL to construct.
	assert.Empty(t, TailscaleSettings{}.TailnetURL("books"))
}

func TestDocktailLabelsWhenEnabled(t *testing.T) {
	labels := docktailLabels("writebook", "once", true, false)

	assert.Equal(t, map[string]string{
		"docktail.service.enable":           "true",
		"docktail.service.name":             "writebook",
		"docktail.service.port":             "80",
		"docktail.service.service-protocol": "https",
		"docktail.service.network":          "once",
	}, labels)
}

func TestDocktailLabelsWithFunnel(t *testing.T) {
	labels := docktailLabels("writebook", "once", true, true)

	assert.Equal(t, "true", labels["docktail.service.enable"])
	assert.Equal(t, "true", labels["docktail.funnel.enable"])
	assert.Equal(t, "80", labels["docktail.funnel.port"])
}

func TestDocktailLabelsWhenDisabled(t *testing.T) {
	assert.Nil(t, docktailLabels("writebook", "once", false, false))
	// Funnel only matters when Tailscale is enabled.
	assert.Nil(t, docktailLabels("writebook", "once", false, true))
}

func TestValidateFunnelDuration(t *testing.T) {
	assert.NoError(t, ValidateFunnelDuration(DefaultFunnelDuration))
	assert.NoError(t, ValidateFunnelDuration(MaxFunnelDuration))
	assert.ErrorIs(t, ValidateFunnelDuration(0), ErrFunnelDurationInvalid)
	assert.ErrorIs(t, ValidateFunnelDuration(-time.Minute), ErrFunnelDurationInvalid)
	assert.ErrorIs(t, ValidateFunnelDuration(MaxFunnelDuration+time.Minute), ErrFunnelDurationTooLong)
}

func TestContainerConfigInjectsDocktailLabels(t *testing.T) {
	app := testApplication("writebook")

	enabled := app.containerConfig(nil, true)
	assert.Equal(t, app.Settings.Marshal(), enabled.Labels[labelKey])
	assert.Equal(t, "true", enabled.Labels["docktail.service.enable"])
	assert.Equal(t, "writebook", enabled.Labels["docktail.service.name"])
	assert.Equal(t, "once-test", enabled.Labels["docktail.service.network"])

	disabled := app.containerConfig(nil, false)
	assert.Equal(t, app.Settings.Marshal(), disabled.Labels[labelKey])
	assert.NotContains(t, disabled.Labels, "docktail.service.enable")
}

func TestContainerConfigRespectsPerAppExposure(t *testing.T) {
	app := testApplication("writebook")
	app.Settings.TailscaleExcluded = true

	// Even with Tailscale globally enabled, an opted-out app gets no docktail labels.
	cfg := app.containerConfig(nil, true)
	assert.NotContains(t, cfg.Labels, "docktail.service.enable")
	assert.Equal(t, app.Settings.Marshal(), cfg.Labels[labelKey])
}

func TestContainerConfigFunnelLabelTracksSettings(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	app := testApplication("writebook")
	app.Settings.FunnelExpiresAt = &expires

	withFunnel := app.containerConfig(nil, true)
	assert.Equal(t, "true", withFunnel.Labels["docktail.funnel.enable"])

	app.Settings.FunnelExpiresAt = nil
	withoutFunnel := app.containerConfig(nil, true)
	assert.NotContains(t, withoutFunnel.Labels, "docktail.funnel.enable")
}

func TestScanRegistrationError(t *testing.T) {
	logs := `2026-06-27 INF tsdproxy starting
2026-06-27 INF watching docker labels
2026-06-27 ERR register failed error="requested tags are invalid or not permitted: tag:once"
2026-06-27 INF retrying`

	// Returns tsdproxy's own rejection line so the user sees the real reason.
	assert.Equal(t, `2026-06-27 ERR register failed error="requested tags are invalid or not permitted: tag:once"`,
		scanRegistrationError(logs))

	// Most recent matching line wins when registration is retried repeatedly.
	repeated := logs + "\n2026-06-27 ERR register failed error=\"tags are invalid or not permitted: tag:two\""
	assert.Contains(t, scanRegistrationError(repeated), "tag:two")

	// Healthy logs yield no error to surface.
	assert.Empty(t, scanRegistrationError("2026-06-27 INF node registered as once-admin\n"))
}

func TestTSDProxyLabels(t *testing.T) {
	labels := tsdproxyLabels("writebook", true, false)
	assert.Equal(t, "true", labels["tsdproxy.enable"])
	assert.Equal(t, "80/http:80/http", labels["tsdproxy.port.1"])

	assert.Equal(t, "443/https:80/http, tailscale_funnel", tsdproxyLabels("writebook", true, true)["tsdproxy.port.1"])
	assert.Nil(t, tsdproxyLabels("writebook", false, false))
}

// Helpers

// testApplication builds an app attached to a namespace whose network name the
// docktail labels reference. No Docker client is needed for label assembly.
func testApplication(name string) *Application {
	return &Application{
		namespace: &Namespace{name: "once-test"},
		Settings:  ApplicationSettings{Name: name, Image: name + ":1"},
	}
}
