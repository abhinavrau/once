package docker

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateTailscaleServiceName(t *testing.T) {
	for _, name := range []string{"writebook", "app-2", "a", "grafana-01"} {
		assert.NoError(t, ValidateTailscaleServiceName(name))
	}

	for _, name := range []string{"", "My_App", "app.name", "-app", "app-", "APP", strings.Repeat("a", 64)} {
		err := ValidateTailscaleServiceName(name)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "not a valid DNS label")
	}
}

func TestShadowWarnings(t *testing.T) {
	daemon := &FakeTailscaleDaemon{StatusJSON: `{
		"Self": {"HostName": "once-tailscale", "DNSName": "once-tailscale.tail1234.ts.net."},
		"Peer": {
			"nodekey:aaa": {"HostName": "Grafana-Box", "DNSName": "grafana.tail1234.ts.net."},
			"nodekey:bbb": {"HostName": "laptop", "DNSName": ""}
		}
	}`}

	warnings := shadowWarnings(t.Context(), daemon, []string{"grafana", "writebook", "laptop"})
	require.Len(t, warnings, 2)
	assert.Contains(t, warnings[0], `machine "grafana"`)
	assert.Contains(t, warnings[0], `service "grafana"`)
	assert.Contains(t, warnings[1], `machine "laptop"`)
}

func TestShadowWarningsBestEffort(t *testing.T) {
	daemon := &FakeTailscaleDaemon{StatusErr: assert.AnError}
	assert.Empty(t, shadowWarnings(t.Context(), daemon, []string{"grafana"}))
}

func TestEnableTailscaleRejectsInvalidServiceNames(t *testing.T) {
	ns := &Namespace{name: "test"}
	ns.applications = append(ns.applications, NewApplication(ns, ApplicationSettings{Name: "my_app"}))

	_, err := ns.EnableTailscale(t.Context(), TailscaleSettings{ClientID: "id", ClientSecret: "secret"})
	require.ErrorContains(t, err, `app name "my_app" is not a valid DNS label`)

	// An excluded app's name is never advertised, so it must not block enable
	// on its own. (Enable then proceeds to the daemon precondition.)
	ns.applications[0].Settings.TailscaleExcluded = true
	ns.admin = NewAdmin(ns)
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", t.TempDir())
	_, err = ns.EnableTailscale(t.Context(), TailscaleSettings{ClientID: "id", ClientSecret: "secret"})
	require.ErrorContains(t, err, "once-admin socket")
}
