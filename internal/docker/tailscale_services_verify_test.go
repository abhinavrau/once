package docker

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWaitForAdvertisedAllServicesPresent(t *testing.T) {
	daemon := &FakeTailscaleDaemon{
		ServeJSON: `{"Services":{"svc:whoami":{},"svc:once-admin":{}}}`,
	}

	err := waitForAdvertised(context.Background(), daemon, []string{"whoami", "once-admin"}, testLogsHint, time.Second, time.Millisecond, nil)
	require.NoError(t, err)
}

func TestWaitForAdvertisedAnnouncesPhaseOnce(t *testing.T) {
	daemon := &FakeTailscaleDaemon{
		ServeJSON: `{"Services":{"svc:whoami":{},"svc:once-admin":{}}}`,
	}

	var lines []string
	err := waitForAdvertised(context.Background(), daemon, []string{"whoami", "once-admin"}, testLogsHint, time.Second, time.Millisecond,
		func(line string) { lines = append(lines, line) })
	require.NoError(t, err)
	require.Len(t, lines, 1, "an immediately-resolved wait must announce exactly once")
	assert.Contains(t, lines[0], "whoami, once-admin")
	assert.Contains(t, lines[0], "1s", "the announcement must state the time bound")
}

func TestWaitForAdvertisedReportsMissingServices(t *testing.T) {
	daemon := &FakeTailscaleDaemon{
		ServeJSON: `{"Services":{"svc:whoami":{}}}`,
	}

	err := waitForAdvertised(context.Background(), daemon, []string{"whoami", "once-admin"}, testLogsHint, 10*time.Millisecond, time.Millisecond, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "once-admin")
	assert.NotContains(t, err.Error(), "whoami,", "advertised services must not be reported missing")
	assert.Contains(t, err.Error(), "once-docktail container logs")
}

func TestWaitForAdvertisedSurfacesDaemonError(t *testing.T) {
	daemon := &FakeTailscaleDaemon{ServeErr: errors.New("no tailscaled socket")}

	err := waitForAdvertised(context.Background(), daemon, []string{"whoami"}, testLogsHint, 10*time.Millisecond, time.Millisecond, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no tailscaled socket")
	assert.NotContains(t, err.Error(), "not yet advertised", "a daemon failure must not read as services being slow")
}

func TestWaitForAdvertisedEmptyServeConfig(t *testing.T) {
	daemon := &FakeTailscaleDaemon{ServeJSON: `{}`}

	err := waitForAdvertised(context.Background(), daemon, []string{"whoami"}, testLogsHint, 10*time.Millisecond, time.Millisecond, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "whoami")
}

func TestRequireServeEnabledSurfacesTheEnablementLink(t *testing.T) {
	daemon := &FakeTailscaleDaemon{ProbeOutput: serveDisabledOutput}

	err := requireServeEnabled(context.Background(), daemon)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Tailscale Serve is not enabled on this tailnet")
	assert.Contains(t, err.Error(), "https://login.tailscale.com/f/serve?node=nodeIDabc123CNTRL")
	assert.Contains(t, err.Error(), "once tailscale enable")
}

func TestRequireServeEnabledPassesWhenServeIsEnabled(t *testing.T) {
	daemon := &FakeTailscaleDaemon{ProbeOutput: serveEnabledOutput}

	require.NoError(t, requireServeEnabled(context.Background(), daemon))
}

// An unrunnable probe says nothing about the tailnet, so it must not block the
// enable with a diagnosis it cannot support.
func TestRequireServeEnabledIgnoresProbeFailures(t *testing.T) {
	daemon := &FakeTailscaleDaemon{ProbeErr: errors.New("no tailscaled socket")}

	require.NoError(t, requireServeEnabled(context.Background(), daemon))
}

func TestWaitForApprovedAnnouncesPhaseOnce(t *testing.T) {
	api := approvalAPI(t, map[string]string{
		"whoami": `{"hosts":[{"stableNodeID":"n1","approvalLevel":"approved:auto","configured":"ready"}]}`,
	})

	var lines []string
	err := waitForApprovedAgainst(api, func(line string) { lines = append(lines, line) }, "whoami")
	require.NoError(t, err)
	require.Len(t, lines, 1, "an immediately-resolved wait must announce exactly once")
	assert.Contains(t, lines[0], "whoami")
	assert.Contains(t, lines[0], "50ms", "the announcement must state the time bound")
}

func TestWaitForApprovedSuccess(t *testing.T) {
	api := approvalAPI(t, map[string]string{
		"whoami":     `{"hosts":[{"stableNodeID":"n1","approvalLevel":"approved:auto","configured":"ready"}]}`,
		"once-admin": `{"hosts":[{"stableNodeID":"n1","approvalLevel":"approved:manual","configured":"ready"}]}`,
	})

	require.NoError(t, waitForApprovedAgainst(api, nil, "whoami", "once-admin"))

	// The devices reads must be byte-compatible with Docktail's listServiceHosts
	// requests: GET /tailnet/-/services/svc:<name>/devices.
	assert.Equal(t, []string{
		"/tailnet/-/services/svc:whoami/devices",
		"/tailnet/-/services/svc:once-admin/devices",
	}, api.paths(http.MethodGet))
}

func TestWaitForApprovedPendingApprovalFailsFast(t *testing.T) {
	api := approvalAPI(t, map[string]string{
		"whoami": `{"hosts":[{"stableNodeID":"n1","approvalLevel":"not-approved","configured":"ready"}]}`,
	})

	err := waitForApprovedAgainst(api, nil, "whoami")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"whoami"`)
	assert.Contains(t, err.Error(), "pending approval")
	assert.Contains(t, err.Error(), "autoApprovers.services")
	assert.Contains(t, err.Error(), "tag:once")
	assert.Equal(t, 1, api.callCount(), "pending approval must fail immediately, not poll until timeout")
}

func TestWaitForApprovedServiceNeverAppears(t *testing.T) {
	api := approvalAPI(t, map[string]string{"whoami": `{"hosts":[]}`})

	err := waitForApprovedAgainst(api, nil, "whoami")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "never appeared in the tailnet: whoami")
	assert.Greater(t, api.callCount(), 1, "an unknown service is polled until the deadline")
}

func TestWaitForApprovedMissingServiceDefinitionPollsThenFails(t *testing.T) {
	// Docktail has not defined the Service yet: every devices read is a 404.
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodGet, serviceDevicesPath("whoami"), http.StatusNotFound, "")

	err := waitForApprovedAgainst(api, nil, "whoami")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "never appeared in the tailnet: whoami")
	assert.Greater(t, api.callCount(), 1, "a missing definition is polled until the deadline")
}

func TestWaitForApprovedSurfacesAPIErrors(t *testing.T) {
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodGet, serviceDevicesPath("whoami"), http.StatusForbidden, `{"message":"boom"}`)

	err := waitForApprovedAgainst(api, nil, "whoami")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `service "whoami"`)
	assert.Contains(t, err.Error(), "boom")
}

func TestWaitForApprovedSurfacesTransportErrors(t *testing.T) {
	api := newFakeTailscaleAPI(t)
	unreachable := errors.New("contacting the Tailscale API: connection refused")
	api.fail(http.MethodGet, serviceDevicesPath("whoami"), unreachable)

	err := waitForApprovedAgainst(api, nil, "whoami")
	require.ErrorIs(t, err, unreachable)
	assert.Equal(t, 1, api.callCount(), "a transport failure must abort the wait, not poll until the deadline")
}

func TestVerifyServicesSkipsControlSeam(t *testing.T) {
	t.Setenv(envControlURL, "http://headscale:8080")
	t.Setenv(envAuthKey, "tskey-auth-abc")

	// Services do not exist on headscale; the adapter's verify must
	// short-circuit before touching Docker or any API.
	err := (&servicesTopology{}).verify(context.Background(), TailscaleSettings{}, []string{"whoami"}, nil)
	require.NoError(t, err)
}

func TestVerifyServicesNoExpectedServices(t *testing.T) {
	// The facade's own shortcut, ahead of resolving any topology.
	err := (&Tailscale{}).Verify(context.Background(), nil, nil)
	require.NoError(t, err)
}

// Helpers

// serviceDevicesPath is the listServiceHosts path Docktail reads a Service's
// advertising devices from, and so must Once.
func serviceDevicesPath(name string) string {
	return "/tailnet/-/services/svc:" + name + "/devices"
}

// approvalAPI cans one listServiceHosts response per service name, so the
// approval checks parse the same JSON shape the real API returns.
func approvalAPI(t *testing.T, hosts map[string]string) *fakeTailscaleAPI {
	t.Helper()
	api := newFakeTailscaleAPI(t)
	for name, body := range hosts {
		api.reply(http.MethodGet, serviceDevicesPath(name), http.StatusOK, body)
	}
	return api
}

func waitForApprovedAgainst(api tailscaleAPI, announce func(string), expected ...string) error {
	return waitForApproved(context.Background(), api,
		TailscaleSettings{ClientID: "id", ClientSecret: "secret", Tag: "tag:once"},
		expected, testLogsHint, 50*time.Millisecond, time.Millisecond, announce)
}
