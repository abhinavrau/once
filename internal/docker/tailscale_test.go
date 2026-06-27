package docker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTailscaleSettingsRoundTrip(t *testing.T) {
	settings := TailscaleSettings{ClientID: "id-123", ClientSecret: "secret-456", APIKey: "key-789", DomainSuffix: "tailnet-name.ts.net"}

	parsed, err := UnmarshalTailscaleSettings(settings.Marshal())
	require.NoError(t, err)
	assert.Equal(t, settings, parsed)
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

func TestDomainSuffixFromURL(t *testing.T) {
	assert.Equal(t, "tailnet-name.ts.net", domainSuffixFromURL("https://writebook.tailnet-name.ts.net"))
	assert.Equal(t, "tailnet.test", domainSuffixFromURL("http://whoami.tailnet.test:8080"))
	assert.Empty(t, domainSuffixFromURL("https://singlelabel"))
	assert.Empty(t, domainSuffixFromURL(""))
}

func TestTailnetURL(t *testing.T) {
	assert.Equal(t, "https://books.tailnet-name.ts.net",
		TailscaleSettings{DomainSuffix: "tailnet-name.ts.net"}.TailnetURL("books"))
	// No suffix known yet -> no URL to construct.
	assert.Empty(t, TailscaleSettings{}.TailnetURL("books"))
}

func TestTSDProxyLabelsWhenEnabled(t *testing.T) {
	labels := tsdproxyLabels("writebook", true, false)

	assert.Equal(t, map[string]string{
		"tsdproxy.enable":    "true",
		"tsdproxy.name":      "writebook",
		"tsdproxy.port.1":    "80/http:80/http",
		"tsdproxy.ephemeral": "true",
	}, labels)
}

func TestTSDProxyLabelsWithFunnel(t *testing.T) {
	labels := tsdproxyLabels("writebook", true, true)

	assert.Equal(t, "443/https:80/http, tailscale_funnel", labels["tsdproxy.port.1"])
	assert.Equal(t, "true", labels["tsdproxy.enable"])
}

func TestTSDProxyLabelsWhenDisabled(t *testing.T) {
	assert.Nil(t, tsdproxyLabels("writebook", false, false))
	// Funnel only matters when Tailscale is enabled.
	assert.Nil(t, tsdproxyLabels("writebook", false, true))
}

func TestValidateFunnelDuration(t *testing.T) {
	assert.NoError(t, ValidateFunnelDuration(DefaultFunnelDuration))
	assert.NoError(t, ValidateFunnelDuration(MaxFunnelDuration))
	assert.ErrorIs(t, ValidateFunnelDuration(0), ErrFunnelDurationInvalid)
	assert.ErrorIs(t, ValidateFunnelDuration(-time.Minute), ErrFunnelDurationInvalid)
	assert.ErrorIs(t, ValidateFunnelDuration(MaxFunnelDuration+time.Minute), ErrFunnelDurationTooLong)
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

func TestContainerConfigRespectsPerAppExposure(t *testing.T) {
	app := &Application{Settings: ApplicationSettings{Name: "writebook", Image: "writebook:1", TailscaleExcluded: true}}

	// Even with Tailscale globally enabled, an opted-out app gets no tsdproxy labels.
	cfg := app.containerConfig(nil, true)
	assert.NotContains(t, cfg.Labels, "tsdproxy.enable")
	assert.Equal(t, app.Settings.Marshal(), cfg.Labels[labelKey])
}

func TestContainerConfigFunnelLabelTracksSettings(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	app := &Application{Settings: ApplicationSettings{Name: "writebook", Image: "writebook:1", FunnelExpiresAt: &expires}}

	withFunnel := app.containerConfig(nil, true)
	assert.Equal(t, "443/https:80/http, tailscale_funnel", withFunnel.Labels["tsdproxy.port.1"])

	app.Settings.FunnelExpiresAt = nil
	withoutFunnel := app.containerConfig(nil, true)
	assert.Equal(t, "80/http:80/http", withoutFunnel.Labels["tsdproxy.port.1"])
}

func TestBuildTSDProxyConfigIncludesAPIKey(t *testing.T) {
	config := buildTSDProxyConfig(TailscaleSettings{ClientID: "id", ClientSecret: "secret", APIKey: "key-789"}, "", "")

	assert.Contains(t, config, `apiKey: "key-789"`)
}

func TestFetchProxies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/proxies", r.URL.Path)
		assert.Equal(t, "Bearer key-789", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"proxies":[
			{"name":"writebook","url":"https://writebook.tailnet.ts.net","status":"Running","ports":[{"funnel":true}]},
			{"name":"books","url":"https://books.tailnet.ts.net","status":"Stopped","ports":[{"funnel":false}]}
		]}`))
	}))
	defer srv.Close()

	proxies, err := fetchProxies(context.Background(), srv.URL, "key-789")
	require.NoError(t, err)

	assert.Equal(t, []TailnetProxy{
		{Name: "writebook", URL: "https://writebook.tailnet.ts.net", Status: "Running", Funnel: true},
		{Name: "books", URL: "https://books.tailnet.ts.net", Status: "Stopped", Funnel: false},
	}, proxies)
}

func TestFetchProxiesUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := fetchProxies(context.Background(), srv.URL, "wrong-key")
	require.Error(t, err)
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

func TestBuildTSDProxyConfigOAuth(t *testing.T) {
	config := buildTSDProxyConfig(TailscaleSettings{ClientID: "id-123", ClientSecret: "secret-456", Tag: "tag:once"}, "", "")

	assert.Contains(t, config, `clientId: "id-123"`)
	assert.Contains(t, config, `clientSecret: "secret-456"`)
	// OAuth requires a tag tsdproxy stamps on its minted keys, else it errors
	// "must define tags to use OAuth" and never registers.
	assert.Contains(t, config, `tags: "tag:once"`)
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
