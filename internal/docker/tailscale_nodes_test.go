package docker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// newTSDProxyReaderFromFixture serves a canned /api/v1/proxies body over a
// real HTTP server, so the reader's tests exercise the exact production
// request/decode path (parseTSDProxyProxies included), not just the parser.
func newTSDProxyReaderFromFixture(t *testing.T, body string) *tsdproxyReader {
	return newTSDProxyReaderFromStatus(t, http.StatusOK, body)
}

func newTSDProxyReaderFromStatus(t *testing.T, status int, body string) *tsdproxyReader {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/proxies", r.URL.Path)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return newTSDProxyReader(server.URL)
}

func TestNodesAppLabelsPlain(t *testing.T) {
	n := newNodesTopology(&Namespace{name: "once"})

	labels := n.appLabels("whoami", "once", 0)

	assert.Equal(t, map[string]string{
		"tsdproxy.enable": "true",
		"tsdproxy.name":   "whoami",
		"tsdproxy.port.1": "443/https:80/http, no_autodetect",
	}, labels)
}

// TestNodesAppLabelsPlainHTTPOnControlSeam pins the headscale-only variant:
// tsnet refuses to listen TLS without a `tailscale cert` capability the
// Control Seam does not have ("you must enable HTTPS in the admin panel"),
// so every node under the seam listens plain HTTP instead.
func TestNodesAppLabelsPlainHTTPOnControlSeam(t *testing.T) {
	t.Setenv("ONCE_TAILSCALE_AUTH_KEY", "tskey-abc")
	t.Setenv("ONCE_TAILSCALE_CONTROL_URL", "http://headscale:8080")
	n := newNodesTopology(&Namespace{name: "once"})

	labels := n.appLabels("whoami", "once", 0)

	assert.Equal(t, "80/http:80/http, no_autodetect", labels["tsdproxy.port.1"])
}

func TestNodesAppLabelsWithFunnel(t *testing.T) {
	n := newNodesTopology(&Namespace{name: "once"})

	labels := n.appLabels("whoami", "once", nodesFunnelPort)

	assert.Equal(t, "443/https:80/http, no_autodetect, tailscale_funnel", labels["tsdproxy.port.1"])
}

func TestValidateModeTopologyCombinationRejectsModeUnderNodes(t *testing.T) {
	err := validateModeTopologyCombination(TailscaleModeHost, TailnetTopologyNodes)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--mode")
	assert.Contains(t, err.Error(), TailnetTopologyNodes)
}

func TestValidateModeTopologyCombinationAllowsEmptyModeUnderNodes(t *testing.T) {
	assert.NoError(t, validateModeTopologyCombination("", TailnetTopologyNodes))
}

func TestValidateModeTopologyCombinationAllowsModeUnderServices(t *testing.T) {
	assert.NoError(t, validateModeTopologyCombination(TailscaleModeSidecar, TailnetTopologyServices))
}

func TestNodesFunnelPortNeedsNoAllocation(t *testing.T) {
	n := newNodesTopology(&Namespace{name: "once"})
	self := &Application{Settings: ApplicationSettings{Name: "whoami"}}

	port, err := n.funnelPort(self, nil)

	require.NoError(t, err)
	assert.Equal(t, nodesFunnelPort, port)
}

func TestNodesLogsHint(t *testing.T) {
	n := newNodesTopology(&Namespace{name: "once"})
	assert.Equal(t, "the once-tsdproxy container logs", n.logsHint())
}

func TestNodesFacts(t *testing.T) {
	n := newNodesTopology(&Namespace{name: "once"})

	facts := n.facts()

	assert.Equal(t, TailnetTopologyNodes, facts.DefaultSettings.Topology)
	assert.Equal(t, "tag:once", facts.DefaultSettings.Tag)
	assert.ElementsMatch(t, []string{"auth_keys:write", "devices:core:write", "policy_file:read", "devices:posture_attributes:read"}, facts.OAuthScopes)
	assert.Nil(t, facts.ModeChoices, "nodes has no Mode - there is nothing to choose")
	assert.NotEmpty(t, facts.SetupIntro)
}

func TestNodesPolicySnippetHasNoServicesMachinery(t *testing.T) {
	snippet := NodesPolicySnippet("tag:once")

	assert.Contains(t, snippet, "tagOwners")
	assert.Contains(t, snippet, "nodeAttrs")
	assert.Contains(t, snippet, "funnel")
	assert.NotContains(t, snippet, "autoApprovers", "nodes has no Services to approve")
	assert.NotContains(t, snippet, "grants", "a tagged node is an ordinary device, not a Service needing its own grant")
}

func TestTopologyRoutesTailnetAppsViaProxy(t *testing.T) {
	assert.True(t, topologyRoutesTailnetAppsViaProxy(TailnetTopologyNodes))
	assert.False(t, topologyRoutesTailnetAppsViaProxy(TailnetTopologyServices))
}

// TestNodesBuildConfigTargetsOnceProxy pins the one setting that routes every
// app's traffic through the Proxy: the Docker provider's targetHostname.
func TestNodesBuildConfigTargetsOnceProxy(t *testing.T) {
	n := newNodesTopology(&Namespace{name: "once"})

	raw, err := n.buildConfig(TailscaleSettings{ClientID: "id", ClientSecret: "secret", Tag: "tag:once"})
	require.NoError(t, err)

	var cfg tsdproxyConfigFile
	require.NoError(t, yaml.Unmarshal(raw, &cfg))

	assert.Equal(t, "once-proxy", cfg.Docker["local"].TargetHostname)
	assert.Equal(t, "unix:///var/run/docker.sock", cfg.Docker["local"].Host)
	assert.True(t, cfg.AdminAllowLocalhost)
	assert.Equal(t, tsdproxyAdminContainerPort, cfg.HTTP.Port)

	provider := cfg.Tailscale.Providers["default"]
	assert.Equal(t, "id", provider.ClientID)
	assert.Equal(t, "secret", provider.ClientSecret)
	assert.Equal(t, "tag:once", provider.Tags)
	assert.Empty(t, provider.AuthKey, "OAuth and the control seam's auth key must never both be written")
}

func TestNodesBuildConfigUsesControlSeamWhenActive(t *testing.T) {
	t.Setenv("ONCE_TAILSCALE_AUTH_KEY", "tskey-abc")
	t.Setenv("ONCE_TAILSCALE_CONTROL_URL", "http://headscale:8080")
	n := newNodesTopology(&Namespace{name: "once"})

	raw, err := n.buildConfig(TailscaleSettings{ClientID: "unused", ClientSecret: "unused", Tag: "tag:once"})
	require.NoError(t, err)

	var cfg tsdproxyConfigFile
	require.NoError(t, yaml.Unmarshal(raw, &cfg))

	provider := cfg.Tailscale.Providers["default"]
	assert.Equal(t, "tskey-abc", provider.AuthKey)
	assert.Equal(t, "http://headscale:8080", provider.ControlURL)
	assert.Empty(t, provider.ClientID, "the control seam must never carry OAuth credentials into the config file")
	assert.Empty(t, provider.ClientSecret)
}

// probeTagPayload is the exact request body TSDProxy's OAuth provider sends
// to mint a per-node key (tailscale/provider.go's getOAuth, v2.3.4):
// non-reusable (one key per node), non-ephemeral, preauthorized, tagged,
// described as "tsdproxy". validateNodesCredentials must send precisely this.
const probeTagPayload = `{"capabilities":{"devices":{"create":{"reusable":false,"ephemeral":false,"preauthorized":true,"tags":["tag:once"]}}},"description":"tsdproxy"}`

const (
	nodesKeysPath   = "/tailnet/-/keys"
	nodesKeyPath    = "/tailnet/-/keys/tskey-id"
	nodesDevicePath = "/tailnet/-/devices"
)

// TestValidateNodesCredentialsOverRealHTTPMatchesTSDProxysWireFormat proves
// probeTagPayload's byte-compatibility claim against the real transport, not
// just the fake seam: run the probe over httptest.NewServer through the
// production httpTailscaleAPI, and inspect the exact bytes and Content-Type
// that reached the wire.
func TestValidateNodesCredentialsOverRealHTTPMatchesTSDProxysWireFormat(t *testing.T) {
	s := newAPIServer(t)
	s.callBody = `{"id":"tskey-id","key":"tskey-secret"}`

	err := validateNodesCredentials(context.Background(), s.api(), TailscaleSettings{Tag: "tag:once"})
	require.NoError(t, err)

	require.NotEmpty(t, s.calls)
	mint := s.calls[0]
	assert.Equal(t, http.MethodPost, mint.method)
	assert.Equal(t, nodesKeysPath, mint.path)
	assert.Equal(t, probeTagPayload, mint.payload)
	assert.Equal(t, "application/json", mint.contentType)
	assert.Equal(t, "Bearer tskey-api-test", mint.auth)
}

func nodesProbeAPI(t *testing.T) *fakeTailscaleAPI {
	t.Helper()
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodPost, nodesKeysPath, http.StatusOK, `{"id":"tskey-id","key":"tskey-secret"}`)
	api.reply(http.MethodDelete, nodesKeyPath, http.StatusOK, `{}`)
	api.reply(http.MethodGet, nodesDevicePath, http.StatusOK, `{"devices":[]}`)
	return api
}

func TestValidateNodesCredentialsSendsTSDProxysExactMintRequest(t *testing.T) {
	api := nodesProbeAPI(t)

	err := validateNodesCredentials(context.Background(), api, TailscaleSettings{Tag: "tag:once"})

	require.NoError(t, err)
	assert.Equal(t, probeTagPayload, api.payload(http.MethodPost, nodesKeysPath))
	assert.True(t, api.requested(http.MethodDelete, nodesKeyPath), "the probe key must be cleaned up")
	assert.True(t, api.requested(http.MethodGet, nodesDevicePath), "the devices:core scope must be proven too")
}

func TestValidateNodesCredentialsRequiresTag(t *testing.T) {
	api := newFakeTailscaleAPI(t)

	err := validateNodesCredentials(context.Background(), api, TailscaleSettings{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "tag")
	assert.Zero(t, api.callCount(), "a missing tag must fail before any API call")
}

func TestValidateNodesCredentialsMissingAuthKeysScope(t *testing.T) {
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodPost, nodesKeysPath, http.StatusForbidden, `{"message":"calling actor does not have enough permissions"}`)

	err := validateNodesCredentials(context.Background(), api, TailscaleSettings{Tag: "tag:once"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth_keys")
	assert.False(t, api.requested(http.MethodGet, nodesDevicePath), "the devices probe must not run after a scope failure")
}

func TestValidateNodesCredentialsRejectsUnownedTag(t *testing.T) {
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodPost, nodesKeysPath, http.StatusBadRequest, `{"message":"requested tags are invalid or not permitted"}`)

	err := validateNodesCredentials(context.Background(), api, TailscaleSettings{Tag: "tag:wrong"})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTagNotInPolicy)
}

func TestValidateNodesCredentialsMissingDevicesScope(t *testing.T) {
	api := nodesProbeAPI(t)
	api.reply(http.MethodGet, nodesDevicePath, http.StatusForbidden, `{"message":"calling actor does not have enough permissions"}`)

	err := validateNodesCredentials(context.Background(), api, TailscaleSettings{Tag: "tag:once"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Devices -> Core")
	assert.True(t, api.requested(http.MethodDelete, nodesKeyPath), "the key probe still cleans up before the devices check runs")
}

// validateHost

func TestValidateNodesHostAgainstTailnetRefusesForeignDevice(t *testing.T) {
	reader := &FakeTailnetReader{ServeJSON: `{}`}
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodGet, nodesDevicePath, http.StatusOK,
		`{"devices":[{"nodeId":"n123","hostname":"whoami","tags":["tag:personal"]}]}`)

	err := validateNodesHostAgainstTailnet(context.Background(), reader, api, "whoami", "tag:once")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "whoami")
	assert.Contains(t, err.Error(), "n123")
	assert.False(t, api.requested(http.MethodDelete, "/device/n123"), "a device without Once's tag must never be deleted")
}

// TestValidateNodesHostAgainstTailnetSweepsOwnLeftoverDevice pins the
// leftover sweep: a colliding device carrying Once's own tag is a remnant of
// a failed or removed deploy (TSDProxy's live state was checked first), so
// it is deleted and the deploy proceeds instead of being blocked forever.
func TestValidateNodesHostAgainstTailnetSweepsOwnLeftoverDevice(t *testing.T) {
	reader := &FakeTailnetReader{ServeJSON: `{}`}
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodGet, nodesDevicePath, http.StatusOK,
		`{"devices":[{"nodeId":"n123","hostname":"whoami","tags":["tag:once"]}]}`)
	api.reply(http.MethodDelete, "/device/n123", http.StatusOK, "")

	err := validateNodesHostAgainstTailnet(context.Background(), reader, api, "whoami", "tag:once")

	require.NoError(t, err)
	assert.True(t, api.requested(http.MethodDelete, "/device/n123"))
}

func TestValidateNodesHostAgainstTailnetRefusesWhenLeftoverDeleteFails(t *testing.T) {
	reader := &FakeTailnetReader{ServeJSON: `{}`}
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodGet, nodesDevicePath, http.StatusOK,
		`{"devices":[{"nodeId":"n123","hostname":"whoami","tags":["tag:once"]}]}`)
	api.reply(http.MethodDelete, "/device/n123", http.StatusForbidden, `{"message":"not permitted"}`)

	err := validateNodesHostAgainstTailnet(context.Background(), reader, api, "whoami", "tag:once")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "sweeping it failed")
	assert.Contains(t, err.Error(), "not permitted")
}

// TestValidateNodesHostAgainstTailnetSweepTreatsGoneDeviceAsSwept covers the
// race where the device disappears between the list and the delete: absence
// is what the sweep wants, so it must not refuse over it.
func TestValidateNodesHostAgainstTailnetSweepTreatsGoneDeviceAsSwept(t *testing.T) {
	reader := &FakeTailnetReader{ServeJSON: `{}`}
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodGet, nodesDevicePath, http.StatusOK,
		`{"devices":[{"nodeId":"n123","hostname":"whoami","tags":["tag:once"]}]}`)
	api.reply(http.MethodDelete, "/device/n123", http.StatusNotFound, `{"message":"device not found"}`)

	err := validateNodesHostAgainstTailnet(context.Background(), reader, api, "whoami", "tag:once")

	assert.NoError(t, err)
}

// TestValidateNodesHostAgainstTailnetNeverSweepsWhenReaderIsSilent pins the
// fail-safe: with TSDProxy unreadable the colliding device may be this app's
// own live node, so the sweep must downgrade to the plain refusal.
func TestValidateNodesHostAgainstTailnetNeverSweepsWhenReaderIsSilent(t *testing.T) {
	reader := &FakeTailnetReader{ServeErr: assert.AnError}
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodGet, nodesDevicePath, http.StatusOK,
		`{"devices":[{"nodeId":"n123","hostname":"whoami","tags":["tag:once"]}]}`)

	err := validateNodesHostAgainstTailnet(context.Background(), reader, api, "whoami", "tag:once")

	require.Error(t, err)
	assert.False(t, api.requested(http.MethodDelete, "/device/n123"), "a device that may back a live node must never be deleted")
}

func TestValidateNodesHostAgainstTailnetAllowsFreeHost(t *testing.T) {
	reader := &FakeTailnetReader{ServeJSON: `{}`}
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodGet, nodesDevicePath, http.StatusOK,
		`{"devices":[{"nodeId":"n123","hostname":"someone-elses-laptop","tags":[]}]}`)

	err := validateNodesHostAgainstTailnet(context.Background(), reader, api, "whoami", "tag:once")

	assert.NoError(t, err)
}

// TestValidateNodesHostAgainstTailnetExemptsOwnNode pins the redeploy
// exemption: an app whose own node already advertises this Host under
// TSDProxy is never refused by its own device, so the API is not even asked.
func TestValidateNodesHostAgainstTailnetExemptsOwnNode(t *testing.T) {
	reader := &FakeTailnetReader{ServeJSON: `{"Services":{"svc:whoami":{}}}`}
	reporter := &recordingReporter{}
	api := newFakeTailscaleAPI(reporter)

	err := validateNodesHostAgainstTailnet(context.Background(), reader, api, "whoami", "tag:once")

	require.NoError(t, err)
	assert.Empty(t, reporter.errors, "the devices list must never be called once the reader says this app already joined")
}

func TestValidateNodesHostAgainstTailnetIsBestEffortOnAPIFailure(t *testing.T) {
	reader := &FakeTailnetReader{ServeJSON: `{}`}
	api := newFakeTailscaleAPI(t)
	api.fail(http.MethodGet, nodesDevicePath, assert.AnError)

	err := validateNodesHostAgainstTailnet(context.Background(), reader, api, "whoami", "tag:once")

	assert.NoError(t, err, "an unreadable devices list must not block a deploy credentials already proved could work")
}

// destroy/remove device cleanup

func TestDeleteTaggedDevicesDeletesOnlyMatchingTags(t *testing.T) {
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodGet, nodesDevicePath, http.StatusOK, `{"devices":[
		{"nodeId":"once-1","hostname":"whoami","tags":["tag:once"]},
		{"nodeId":"once-2","hostname":"once-admin","tags":["tag:once","tag:extra"]},
		{"nodeId":"laptop","hostname":"someone-elses-laptop","tags":["tag:personal"]}
	]}`)
	api.reply(http.MethodDelete, "/device/once-1", http.StatusOK, "")
	api.reply(http.MethodDelete, "/device/once-2", http.StatusOK, "")

	deleteTaggedDevices(context.Background(), api, "tag:once", "")

	assert.True(t, api.requested(http.MethodDelete, "/device/once-1"))
	assert.True(t, api.requested(http.MethodDelete, "/device/once-2"))
	assert.False(t, api.requested(http.MethodDelete, "/device/laptop"), "a device without the tag must never be deleted")
}

// TestDeleteTaggedDevicesNarrowsToHostname pins the once-admin sweep's
// scope: a hostname filter must leave every other tagged device alone, the
// services sidecar identity a topology switch keeps included.
func TestDeleteTaggedDevicesNarrowsToHostname(t *testing.T) {
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodGet, nodesDevicePath, http.StatusOK, `{"devices":[
		{"nodeId":"admin-old","hostname":"once-admin","tags":["tag:once"]},
		{"nodeId":"sidecar","hostname":"once-tailscale","tags":["tag:once"]}
	]}`)
	api.reply(http.MethodDelete, "/device/admin-old", http.StatusOK, "")

	deleteTaggedDevices(context.Background(), api, "tag:once", "once-admin")

	assert.True(t, api.requested(http.MethodDelete, "/device/admin-old"))
	assert.False(t, api.requested(http.MethodDelete, "/device/sidecar"), "a hostname-narrowed sweep must not touch other tagged devices")
}

func TestDeleteTaggedDevicesIsBestEffortOnListFailure(t *testing.T) {
	api := newFakeTailscaleAPI(t)
	api.fail(http.MethodGet, nodesDevicePath, assert.AnError)

	assert.NotPanics(t, func() { deleteTaggedDevices(context.Background(), api, "tag:once", "") })
}

func TestBestEffortDeleteTaggedDevicesSkipsOnControlSeam(t *testing.T) {
	t.Setenv("ONCE_TAILSCALE_AUTH_KEY", "tskey-abc")
	t.Setenv("ONCE_TAILSCALE_CONTROL_URL", "http://headscale:8080")
	n := newNodesTopology(&Namespace{name: "once"})

	// No Docker client and no reachable Tailscale API: a real call of either
	// would panic or hang, proving the control-seam guard short-circuits
	// before either is touched.
	n.bestEffortDeleteTaggedDevices(context.Background(), TailscaleSettings{
		ClientID: "id", ClientSecret: "secret", Tag: "tag:once",
	}, "")
}

func TestBestEffortDeleteTaggedDevicesSkipsWithoutCredentials(t *testing.T) {
	n := newNodesTopology(&Namespace{name: "once"})

	n.bestEffortDeleteTaggedDevices(context.Background(), TailscaleSettings{Tag: "tag:once"}, "")
}

// reader

func TestParseTSDProxyProxiesFixture(t *testing.T) {
	fixture := []byte(`{
	  "proxies": [
	    {
	      "name": "whoami",
	      "status": "Running",
	      "url": "https://whoami.tail1234.ts.net",
	      "ports": [{"name": "443/https", "funnel": false}]
	    },
	    {
	      "name": "once-admin",
	      "status": "Running",
	      "url": "https://once-admin.tail1234.ts.net",
	      "ports": [{"name": "443/https", "funnel": true}]
	    },
	    {
	      "name": "starting-app",
	      "status": "Starting",
	      "url": "",
	      "ports": [{"name": "443/https", "funnel": false}]
	    }
	  ]
	}`)

	proxies, err := parseTSDProxyProxies(fixture)

	require.NoError(t, err)
	require.Len(t, proxies, 3)
	assert.Equal(t, "whoami", proxies[0].Name)
	assert.Equal(t, tsdproxyStatusRunning, proxies[0].Status)
	assert.False(t, hasFunnel(proxies[0].Ports))
	assert.True(t, hasFunnel(proxies[1].Ports))
	assert.Equal(t, "Starting", proxies[2].Status)
}

func TestTSDProxyReaderServeStatusFromFixture(t *testing.T) {
	reader := newTSDProxyReaderFromFixture(t, `{
	  "proxies": [
	    {"name": "whoami", "status": "Running", "url": "https://whoami.tail1234.ts.net", "ports": [{"funnel": false}]},
	    {"name": "once-admin", "status": "Running", "url": "https://once-admin.tail1234.ts.net", "ports": [{"funnel": true}]},
	    {"name": "starting-app", "status": "Starting", "url": "", "ports": [{"funnel": false}]}
	  ]
	}`)

	status, err := reader.ServeStatus(context.Background())

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"whoami", "once-admin"}, status.Services, "a proxy that hasn't joined yet is not advertised")
	assert.Equal(t, map[string]string{"once-admin": "once-admin.tail1234.ts.net:443"}, status.Funnels)
}

func TestTSDProxyReaderMagicDNSSuffixFromFirstJoinedNode(t *testing.T) {
	reader := newTSDProxyReaderFromFixture(t, `{
	  "proxies": [
	    {"name": "starting-app", "status": "Starting", "url": "", "ports": []},
	    {"name": "whoami", "status": "Running", "url": "https://whoami.tail1234.ts.net", "ports": []}
	  ]
	}`)

	suffix, err := reader.MagicDNSSuffix(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "tail1234.ts.net", suffix)
}

func TestTSDProxyReaderMagicDNSSuffixEmptyWhenNothingHasJoined(t *testing.T) {
	reader := newTSDProxyReaderFromFixture(t, `{"proxies": [{"name": "whoami", "status": "Starting", "url": "", "ports": []}]}`)

	suffix, err := reader.MagicDNSSuffix(context.Background())

	require.NoError(t, err)
	assert.Empty(t, suffix)
}

func TestTSDProxyReaderMachineHostnamesAlwaysEmpty(t *testing.T) {
	reader := newTSDProxyReaderFromFixture(t, `{"proxies": [{"name": "whoami", "status": "Running", "url": "https://whoami.tail1234.ts.net", "ports": []}]}`)

	hostnames, err := reader.MachineHostnames(context.Background())

	require.NoError(t, err)
	assert.Empty(t, hostnames)
}

func TestTSDProxyReaderSurfacesHTTPErrors(t *testing.T) {
	reader := newTSDProxyReaderFromStatus(t, http.StatusForbidden, `{"message":"access denied","code":403}`)

	_, err := reader.ServeStatus(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
}

// nodesRouteHost

// TestNodesRouteHostRefusesUnknownSuffix pins the fix for the silent-404 bug:
// a nodes route registered on a bare Host matches nothing TSDProxy ever
// forwards (it always sends the node's FQDN), and nothing re-registers it
// later, so the app is unreachable forever. Refusing beats registering it.
func TestNodesRouteHostRefusesUnknownSuffix(t *testing.T) {
	_, err := nodesRouteHost("vaultwarden", "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "vaultwarden")
}

func TestNodesRouteHostJoinsNameToSuffix(t *testing.T) {
	host, err := nodesRouteHost("vaultwarden", "tail1234.ts.net")

	require.NoError(t, err)
	assert.Equal(t, "vaultwarden.tail1234.ts.net", host)
}

// joinLateReader reports no Magic DNS suffix until the node it is waiting on
// has joined, the way TSDProxy's API reads while a fresh node is still
// walking Initializing -> Starting -> Authenticating -> Running.
type joinLateReader struct {
	FakeTailnetReader
	readsBeforeJoin int
	reads           int
}

func (r *joinLateReader) MagicDNSSuffix(ctx context.Context) (string, error) {
	r.reads++
	if r.reads <= r.readsBeforeJoin {
		return "", nil
	}
	return "tail1234.ts.net", nil
}

// TestWaitForMagicDNSSuffixOutlastsANodeStillJoining is the other half of the
// fix: the first tailnet app under nodes is routinely the first node to join
// anywhere, so the suffix read at deploy time races the join it depends on.
// A single read returns "" and loses that race; the wait does not.
func TestWaitForMagicDNSSuffixOutlastsANodeStillJoining(t *testing.T) {
	reader := &joinLateReader{readsBeforeJoin: 2}

	immediate, err := reader.MagicDNSSuffix(t.Context())
	require.NoError(t, err)
	require.Empty(t, immediate, "a node still joining reports no suffix - what the bug registered a bare route from")

	suffix, err := waitForMagicDNSSuffix(t.Context(), reader, time.Second, time.Millisecond)

	require.NoError(t, err)
	assert.Equal(t, "tail1234.ts.net", suffix)
}

func TestWaitForMagicDNSSuffixGivesUpWhenNothingEverJoins(t *testing.T) {
	reader := &FakeTailnetReader{StatusJSON: `{}`}

	suffix, err := waitForMagicDNSSuffix(t.Context(), reader, 10*time.Millisecond, time.Millisecond)

	require.NoError(t, err)
	assert.Empty(t, suffix)
}
