package docker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWaitForAdvertisedAllServicesPresent(t *testing.T) {
	daemon := &FakeTailscaleDaemon{
		ServeJSON: `{"Services":{"svc:whoami":{},"svc:once-admin":{}}}`,
	}

	err := waitForAdvertised(context.Background(), daemon, []string{"whoami", "once-admin"}, time.Second, time.Millisecond, nil)
	require.NoError(t, err)
}

func TestWaitForAdvertisedAnnouncesPhaseOnce(t *testing.T) {
	daemon := &FakeTailscaleDaemon{
		ServeJSON: `{"Services":{"svc:whoami":{},"svc:once-admin":{}}}`,
	}

	var lines []string
	err := waitForAdvertised(context.Background(), daemon, []string{"whoami", "once-admin"}, time.Second, time.Millisecond,
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

	err := waitForAdvertised(context.Background(), daemon, []string{"whoami", "once-admin"}, 10*time.Millisecond, time.Millisecond, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "once-admin")
	assert.NotContains(t, err.Error(), "whoami,", "advertised services must not be reported missing")
	assert.Contains(t, err.Error(), "once-docktail container logs")
}

func TestWaitForAdvertisedSurfacesDaemonError(t *testing.T) {
	daemon := &FakeTailscaleDaemon{ServeErr: errors.New("no tailscaled socket")}

	err := waitForAdvertised(context.Background(), daemon, []string{"whoami"}, 10*time.Millisecond, time.Millisecond, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no tailscaled socket")
	assert.NotContains(t, err.Error(), "not yet advertised", "a daemon failure must not read as services being slow")
}

func TestWaitForAdvertisedEmptyServeConfig(t *testing.T) {
	daemon := &FakeTailscaleDaemon{ServeJSON: `{}`}

	err := waitForAdvertised(context.Background(), daemon, []string{"whoami"}, 10*time.Millisecond, time.Millisecond, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "whoami")
}

// approvalServer stands in for the Tailscale API's OAuth token and
// service-devices endpoints. Hosts are configurable per service name;
// requests are recorded so tests can assert byte-compatibility with the
// requests Docktail sends.
type approvalServer struct {
	srv *httptest.Server

	hosts    map[string]string // service name -> hosts JSON response
	statuses map[string]int    // service name -> devices endpoint status

	devicePaths []string
}

func newApprovalServer(t *testing.T) *approvalServer {
	t.Helper()
	a := &approvalServer{hosts: map[string]string{}, statuses: map[string]int{}}
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			require.NoError(t, r.ParseForm())
			assert.Equal(t, "client_credentials", r.Form.Get("grant_type"))
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "tskey-api-verify"})
			return
		}

		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "Bearer tskey-api-verify", r.Header.Get("Authorization"))
		a.devicePaths = append(a.devicePaths, r.URL.Path)

		for name, hosts := range a.hosts {
			if r.URL.Path == "/tailnet/-/services/svc:"+name+"/devices" {
				if status := a.statuses[name]; status != 0 {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"message":"boom"}`))
					return
				}
				_, _ = w.Write([]byte(hosts))
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(a.srv.Close)
	return a
}

func waitForApprovedAgainst(a *approvalServer, announce func(string), expected ...string) error {
	return waitForApproved(context.Background(), a.srv.URL,
		TailscaleSettings{ClientID: "id", ClientSecret: "secret", Tag: "tag:once"},
		expected, 50*time.Millisecond, time.Millisecond, announce)
}

func TestWaitForApprovedAnnouncesPhaseOnce(t *testing.T) {
	a := newApprovalServer(t)
	a.hosts["whoami"] = `{"hosts":[{"stableNodeID":"n1","approvalLevel":"approved:auto","configured":"ready"}]}`

	var lines []string
	err := waitForApprovedAgainst(a, func(line string) { lines = append(lines, line) }, "whoami")
	require.NoError(t, err)
	require.Len(t, lines, 1, "an immediately-resolved wait must announce exactly once")
	assert.Contains(t, lines[0], "whoami")
	assert.Contains(t, lines[0], "50ms", "the announcement must state the time bound")
}

func TestWaitForApprovedSuccess(t *testing.T) {
	a := newApprovalServer(t)
	a.hosts["whoami"] = `{"hosts":[{"stableNodeID":"n1","approvalLevel":"approved:auto","configured":"ready"}]}`
	a.hosts["once-admin"] = `{"hosts":[{"stableNodeID":"n1","approvalLevel":"approved:manual","configured":"ready"}]}`

	require.NoError(t, waitForApprovedAgainst(a, nil, "whoami", "once-admin"))

	// The devices reads must be byte-compatible with Docktail's listServiceHosts
	// requests: GET /tailnet/-/services/svc:<name>/devices.
	assert.Equal(t, []string{
		"/tailnet/-/services/svc:whoami/devices",
		"/tailnet/-/services/svc:once-admin/devices",
	}, a.devicePaths)
}

func TestWaitForApprovedPendingApprovalFailsFast(t *testing.T) {
	a := newApprovalServer(t)
	a.hosts["whoami"] = `{"hosts":[{"stableNodeID":"n1","approvalLevel":"not-approved","configured":"ready"}]}`

	err := waitForApprovedAgainst(a, nil, "whoami")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"whoami"`)
	assert.Contains(t, err.Error(), "pending approval")
	assert.Contains(t, err.Error(), "autoApprovers.services")
	assert.Contains(t, err.Error(), "tag:once")
	assert.Len(t, a.devicePaths, 1, "pending approval must fail immediately, not poll until timeout")
}

func TestWaitForApprovedServiceNeverAppears(t *testing.T) {
	a := newApprovalServer(t)
	a.hosts["whoami"] = `{"hosts":[]}`

	err := waitForApprovedAgainst(a, nil, "whoami")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "never appeared in the tailnet: whoami")
	assert.Greater(t, len(a.devicePaths), 1, "an unknown service is polled until the deadline")
}

func TestWaitForApprovedMissingServiceDefinitionPollsThenFails(t *testing.T) {
	a := newApprovalServer(t) // no hosts configured: every devices read is a 404

	err := waitForApprovedAgainst(a, nil, "whoami")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "never appeared in the tailnet: whoami")
}

func TestWaitForApprovedSurfacesAPIErrors(t *testing.T) {
	a := newApprovalServer(t)
	a.hosts["whoami"] = `{"hosts":[]}`
	a.statuses["whoami"] = http.StatusForbidden

	err := waitForApprovedAgainst(a, nil, "whoami")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `service "whoami"`)
	assert.Contains(t, err.Error(), "boom")
}

func TestVerifyServicesSkipsControlSeam(t *testing.T) {
	t.Setenv(envControlURL, "http://headscale:8080")
	t.Setenv(envAuthKey, "tskey-auth-abc")

	// Services do not exist on headscale; verification must short-circuit
	// before touching Docker or any API.
	err := (&Tailscale{}).VerifyServices(context.Background(), []string{"whoami"}, nil)
	require.NoError(t, err)
}

func TestVerifyServicesNoExpectedServices(t *testing.T) {
	err := (&Tailscale{}).VerifyServices(context.Background(), nil, nil)
	require.NoError(t, err)
}
