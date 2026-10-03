package docker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCarriesTailnetLabels is how an app or once-admin from an older Once is
// spotted: it lacks a label the active topology would give it now (ADR-0018's
// tsdproxy.proxyprovider), so it is rolled. Labels it carries beyond those are
// the app's own business.
func TestCarriesTailnetLabels(t *testing.T) {
	want := map[string]string{"tsdproxy.enable": "true", "tsdproxy.proxyprovider": "once"}

	assert.True(t, carriesTailnetLabels(map[string]string{"tsdproxy.enable": "true", "tsdproxy.proxyprovider": "once", "once": "{}"}, want))
	assert.False(t, carriesTailnetLabels(map[string]string{"tsdproxy.enable": "true"}, want))
	assert.False(t, carriesTailnetLabels(map[string]string{"tsdproxy.enable": "true", "tsdproxy.proxyprovider": "default"}, want))
	assert.True(t, carriesTailnetLabels(map[string]string{"once": "{}"}, nil))
}

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

func TestSuppressUnusedCredentials(t *testing.T) {
	typed := TailscaleSettings{ClientID: "id", ClientSecret: "secret", Tag: "tag:once", Mode: TailscaleModeSidecar}

	t.Run("under the control seam", func(t *testing.T) {
		t.Setenv(envAuthKey, "tskey-auth-abc")
		t.Setenv(envControlURL, "http://headscale:8080")

		stored := typed.suppressUnusedCredentials()
		assert.Empty(t, stored.ClientID)
		assert.Empty(t, stored.ClientSecret)
		// The tag is deliberately kept: the host-mode pre-flight still reads it.
		assert.Equal(t, "tag:once", stored.Tag)
		assert.Equal(t, TailscaleModeSidecar, stored.Mode)
	})

	t.Run("on the SaaS path", func(t *testing.T) {
		t.Setenv(envAuthKey, "")
		t.Setenv(envControlURL, "")

		assert.Equal(t, typed, typed.suppressUnusedCredentials())
	})
}

func TestControlSeam(t *testing.T) {
	// controlSeam is the only reader of the seam's env vars: it needs both to
	// report active, and controlSeamActive is derived from it rather than
	// re-reading them.
	assertSeam := func(t *testing.T, authKey, controlURL string, wantOK bool) {
		t.Helper()
		t.Setenv(envAuthKey, authKey)
		t.Setenv(envControlURL, controlURL)

		key, url, ok := controlSeam()
		assert.Equal(t, wantOK, ok)
		assert.Equal(t, authKey, key)
		assert.Equal(t, controlURL, url)
		assert.Equal(t, ok, controlSeamActive())
	}

	t.Run("both set", func(t *testing.T) {
		assertSeam(t, "tskey-auth-abc", "http://headscale:8080", true)
	})

	t.Run("auth key missing", func(t *testing.T) {
		assertSeam(t, "", "http://headscale:8080", false)
	})

	t.Run("control URL missing", func(t *testing.T) {
		assertSeam(t, "tskey-auth-abc", "", false)
	})

	t.Run("neither set", func(t *testing.T) {
		assertSeam(t, "", "", false)
	})
}

func TestValidateTailscaleMode(t *testing.T) {
	assert.NoError(t, ValidateTailscaleMode(""))
	assert.NoError(t, ValidateTailscaleMode(TailscaleModeHost))
	assert.NoError(t, ValidateTailscaleMode(TailscaleModeSidecar))
	assert.ErrorContains(t, ValidateTailscaleMode("both"), `invalid Tailscale mode "both"`)
}

func TestResolveModeExplicitWinsWithoutDocker(t *testing.T) {
	// An explicit mode resolves without touching Docker (a nil client would
	// panic), so the host-mode pre-flight still runs before any Docker work.
	s := newServicesTopology(&Namespace{name: "test"})

	mode, fresh, err := s.resolveMode(context.Background(), TailscaleModeHost)
	require.NoError(t, err)
	assert.Equal(t, TailscaleModeHost, mode)
	assert.False(t, fresh)
}

func TestHostModeNotice(t *testing.T) {
	plantHostSocket := func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, tailscaledSocketFile), nil, 0o644))
		t.Setenv("ONCE_TAILSCALED_DIR", dir)
	}

	t.Run("fresh enable alongside a host tailscaled", func(t *testing.T) {
		plantHostSocket(t)
		assert.Contains(t, hostModeNotice("", true), "--mode host")
	})

	t.Run("no host tailscaled", func(t *testing.T) {
		t.Setenv("ONCE_TAILSCALED_DIR", t.TempDir())
		assert.Empty(t, hostModeNotice("", true))
	})

	t.Run("explicit mode", func(t *testing.T) {
		plantHostSocket(t)
		assert.Empty(t, hostModeNotice(TailscaleModeSidecar, true))
		assert.Empty(t, hostModeNotice(TailscaleModeHost, true))
	})

	t.Run("re-enable", func(t *testing.T) {
		plantHostSocket(t)
		assert.Empty(t, hostModeNotice("", false))
	})
}

func TestTailnetURL(t *testing.T) {
	assert.Equal(t, "https://books.tailnet-name.ts.net",
		TailscaleSettings{DomainSuffix: "tailnet-name.ts.net"}.TailnetURL("books"))
	// No suffix known yet -> no URL to construct.
	assert.Empty(t, TailscaleSettings{}.TailnetURL("books"))
}

func TestSettingsFunnelURL(t *testing.T) {
	s := TailscaleSettings{NodeHost: "once-tailscale-1.tailnet-name.ts.net"}
	assert.Equal(t, "https://once-tailscale-1.tailnet-name.ts.net", s.FunnelURL("books", 443, nil))
	assert.Equal(t, "https://once-tailscale-1.tailnet-name.ts.net:8443", s.FunnelURL("books", 8443, nil))
	// No node host, or no active port -> no URL.
	assert.Empty(t, TailscaleSettings{}.FunnelURL("books", 443, nil))
	assert.Empty(t, s.FunnelURL("books", 0, nil))
}

func TestSettingsFunnelURLUnderNodesIsTheAppsOwnTailnetURL(t *testing.T) {
	// Under nodes, every app is its own node: Funnel serves on the app's own
	// tailnet URL, not a shared node host - and 443 is the only port it ever
	// offers Funnel on.
	s := TailscaleSettings{Topology: TailnetTopologyNodes, DomainSuffix: "tailnet-name.ts.net"}
	assert.Equal(t, "https://books.tailnet-name.ts.net", s.FunnelURL("books", 443, nil))
	assert.Empty(t, s.FunnelURL("books", 0, nil))
	assert.Empty(t, TailscaleSettings{Topology: TailnetTopologyNodes}.FunnelURL("books", 443, nil), "no suffix known yet -> no URL")
}

// TestSettingsTailnetURLForPrefersTheLiveFQDN pins the rule every URL path
// shares: a node Tailscale renamed (books -> books-1) answers only to the name
// it was given, so the live FQDN beats the one composed from the app's name.
func TestSettingsTailnetURLForPrefersTheLiveFQDN(t *testing.T) {
	s := TailscaleSettings{Topology: TailnetTopologyNodes, DomainSuffix: "tailnet-name.ts.net"}
	fqdns := map[string]string{"books": "books-1.tailnet-name.ts.net"}

	assert.Equal(t, "https://books-1.tailnet-name.ts.net", s.TailnetURLFor("books", fqdns))
	assert.Equal(t, "https://books-1.tailnet-name.ts.net", s.FunnelURL("books", 443, fqdns))
	assert.Equal(t, "https://chat.tailnet-name.ts.net", s.TailnetURLFor("chat", fqdns), "a name with no live node falls back to the composed URL")
}

func TestDocktailLabelsWhenEnabled(t *testing.T) {
	labels := (&servicesTopology{}).appLabels("writebook", "once", 0)

	assert.Equal(t, map[string]string{
		"docktail.service.enable":           "true",
		"docktail.service.name":             "writebook",
		"docktail.service.port":             "80",
		"docktail.service.service-protocol": "https",
		"docktail.service.network":          "once",
	}, labels)
}

func TestDocktailLabelsWithFunnel(t *testing.T) {
	labels := (&servicesTopology{}).appLabels("writebook", "once", 443)

	assert.Equal(t, "true", labels["docktail.service.enable"])
	assert.Equal(t, "true", labels["docktail.funnel.enable"])
	assert.Equal(t, "80", labels["docktail.funnel.port"])
	assert.Equal(t, "443", labels["docktail.funnel.funnel-port"])

	assert.Equal(t, "8443", (&servicesTopology{}).appLabels("writebook", "once", 8443)["docktail.funnel.funnel-port"])
}

func TestAppLabelsNilWhenDisabled(t *testing.T) {
	// The facade, not the adapter, owns the disabled shortcut: appLabels
	// itself has no notion of "enabled" (ticket 03).
	labels, err := (&Tailscale{}).AppLabels(context.Background(), false, "writebook", "once", 0)
	require.NoError(t, err)
	assert.Nil(t, labels)
	// Funnel only matters when Tailscale is enabled.
	labels, err = (&Tailscale{}).AppLabels(context.Background(), false, "writebook", "once", 443)
	require.NoError(t, err)
	assert.Nil(t, labels)
}

func TestWaitForFunnel(t *testing.T) {
	// writebook is funnelled: the node's funnel-enabled endpoint proxies to the
	// same backend the service does. campfire is advertised but not funnelled.
	serveJSON := `{
		"Web": {"once.tail1234.ts.net:443": {"Handlers": {"/": {"Proxy": "http://10.0.0.5:80"}}}},
		"AllowFunnel": {"once.tail1234.ts.net:443": true},
		"Services": {
			"svc:writebook": {
				"Web": {"writebook.tail1234.ts.net:443": {"Handlers": {"/": {"Proxy": "http://10.0.0.5:80"}}}}
			},
			"svc:campfire": {
				"Web": {"campfire.tail1234.ts.net:443": {"Handlers": {"/": {"Proxy": "http://10.0.0.6:80"}}}}
			}
		}
	}`

	daemon := &FakeTailnetReader{ServeJSON: serveJSON}
	hostPort, err := waitForFunnel(t.Context(), daemon, "writebook", testLogsHint, time.Second, time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, "once.tail1234.ts.net:443", hostPort)

	// Another app's active Funnel must not count as this app's activation.
	_, err = waitForFunnel(t.Context(), daemon, "campfire", testLogsHint, 50*time.Millisecond, 10*time.Millisecond)
	require.ErrorContains(t, err, "funnel did not activate for campfire")
	assert.ErrorContains(t, err, testLogsHint)

	// No funnel at all, or a daemon that keeps erroring, times out the same way.
	daemon = &FakeTailnetReader{ServeJSON: `{}`}
	_, err = waitForFunnel(t.Context(), daemon, "writebook", testLogsHint, 50*time.Millisecond, 10*time.Millisecond)
	require.ErrorContains(t, err, "funnel did not activate")

	daemon = &FakeTailnetReader{ServeErr: assert.AnError}
	_, err = waitForFunnel(t.Context(), daemon, "writebook", testLogsHint, 50*time.Millisecond, 10*time.Millisecond)
	require.ErrorContains(t, err, "funnel did not activate")
}

func TestWaitForSidecarLogin(t *testing.T) {
	running := &FakeTailnetReader{StatusJSON: `{"BackendState": "Running"}`}
	healthy := func(context.Context) (string, bool) { return "", false }

	require.NoError(t, waitForSidecarLogin(t.Context(), running, healthy, time.Second, time.Millisecond))

	// A crash-looping container fails immediately with its message, however
	// long the timeout - that is how a rejected auth key presents.
	crashed := func(context.Context) (string, bool) {
		return "the once-tailscale sidecar keeps exiting; recent logs:\ninvalid key", true
	}
	err := waitForSidecarLogin(t.Context(), running, crashed, time.Hour, time.Millisecond)
	require.ErrorContains(t, err, "invalid key")

	// A daemon that is slow to log in or unreachable is non-fatal at timeout:
	// the suffix and verify waits that follow surface persistent problems.
	slow := &FakeTailnetReader{StatusErr: assert.AnError}
	require.NoError(t, waitForSidecarLogin(t.Context(), slow, healthy, 30*time.Millisecond, 10*time.Millisecond))

	needsLogin := &FakeTailnetReader{StatusJSON: `{"BackendState": "NeedsLogin"}`}
	require.NoError(t, waitForSidecarLogin(t.Context(), needsLogin, healthy, 30*time.Millisecond, 10*time.Millisecond))
}

func TestValidateFunnelDuration(t *testing.T) {
	assert.NoError(t, ValidateFunnelDuration(DefaultFunnelDuration))
	assert.NoError(t, ValidateFunnelDuration(MaxFunnelDuration))
	assert.ErrorIs(t, ValidateFunnelDuration(0), ErrFunnelDurationInvalid)
	assert.ErrorIs(t, ValidateFunnelDuration(-time.Minute), ErrFunnelDurationInvalid)
	assert.ErrorIs(t, ValidateFunnelDuration(MaxFunnelDuration+time.Minute), ErrFunnelDurationTooLong)
}

// tailnetLabelsFor stands in for what the facade's AppLabels hands
// deployWithVolume once Tailscale is enabled: the active topology's
// appLabels for this app's Host, network, and Funnel port.
func tailnetLabelsFor(app *Application) map[string]string {
	return (&servicesTopology{}).appLabels(app.Settings.Host, app.namespace.name, app.Settings.ActiveFunnelPort())
}

func TestContainerConfigInjectsDocktailLabels(t *testing.T) {
	// The advertised service name is the app's Host (the tailnet name knob),
	// not its unique app name.
	app := testTailnetApplication("writebook-abc123", "writebook")

	enabled := app.containerConfig(nil, tailnetLabelsFor(app))
	assert.Equal(t, app.Settings.Marshal(), enabled.Labels[labelKey])
	assert.Equal(t, "true", enabled.Labels["docktail.service.enable"])
	assert.Equal(t, "writebook", enabled.Labels["docktail.service.name"])
	assert.Equal(t, "once-test", enabled.Labels["docktail.service.network"])

	disabled := app.containerConfig(nil, nil)
	assert.Equal(t, app.Settings.Marshal(), disabled.Labels[labelKey])
	assert.NotContains(t, disabled.Labels, "docktail.service.enable")
}

func TestContainerConfigExposureExclusivity(t *testing.T) {
	// Even with Tailscale globally enabled, only tailnet-exposed apps carry
	// docktail labels - deployWithVolume achieves that by only asking
	// AppLabels for a tailnet-exposed app; containerConfig itself just merges
	// whatever it is handed.
	for _, exposure := range []Exposure{ExposureLocal, ExposurePublic} {
		app := testTailnetApplication("writebook", "writebook.localhost")
		app.Settings.Exposure = exposure
		cfg := app.containerConfig(nil, nil)
		assert.NotContains(t, cfg.Labels, "docktail.service.enable")
		assert.Equal(t, app.Settings.Marshal(), cfg.Labels[labelKey])
	}
}

func TestContainerConfigFunnelLabelTracksSettings(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	app := testTailnetApplication("writebook", "writebook")
	app.Settings.FunnelExpiresAt = &expires

	withFunnel := app.containerConfig(nil, tailnetLabelsFor(app))
	assert.Equal(t, "true", withFunnel.Labels["docktail.funnel.enable"])

	app.Settings.FunnelExpiresAt = nil
	withoutFunnel := app.containerConfig(nil, tailnetLabelsFor(app))
	assert.NotContains(t, withoutFunnel.Labels, "docktail.funnel.enable")
}

func TestFunnelURL(t *testing.T) {
	assert.Equal(t, "https://n.ts.net", funnelURL("n.ts.net:443"))
	assert.Equal(t, "https://n.ts.net:8443", funnelURL("n.ts.net:8443"))
}

func TestEnableFunnelRequiresTailnetExposure(t *testing.T) {
	app := testTailnetApplication("writebook", "writebook.localhost")
	app.Settings.Exposure = ExposureLocal

	err := app.EnableFunnel(t.Context(), time.Now().Add(time.Hour))
	assert.ErrorIs(t, err, ErrFunnelRequiresTailnet)
	assert.Nil(t, app.Settings.FunnelExpiresAt)
}

// Helpers

// testLogsHint stands in for what logsHint hands the read paths: the container
// whose logs explain a read that came back short.
const testLogsHint = "the once-docktail container logs"

// testTailnetApplication builds a tailnet-exposed app attached to a namespace
// whose network name the docktail labels reference. No Docker client is needed
// for label assembly.
func testTailnetApplication(name, host string) *Application {
	return &Application{
		namespace: &Namespace{name: "once-test"},
		Settings:  ApplicationSettings{Name: name, Image: name + ":1", Host: host, Exposure: ExposureTailnet},
	}
}
