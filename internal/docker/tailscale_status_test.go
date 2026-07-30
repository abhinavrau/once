package docker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceStatusesApproved(t *testing.T) {
	api := statusAPI(t, map[string]string{
		"whoami": `{"hosts":[{"stableNodeID":"n1","approvalLevel":"approved:auto","configured":"ready"}]}`,
	})
	daemon := &FakeTailscaleDaemon{ServeJSON: `{"Services":{"svc:whoami":{}}}`}

	statuses, err := serviceStatusesAgainst(api, daemon, "whoami")
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.Equal(t, "whoami", statuses[0].Name)
	assert.Equal(t, "https://whoami.tail1234.ts.net", statuses[0].URL)
	assert.Equal(t, "approved", statuses[0].State)
}

func TestServiceStatusesPendingApproval(t *testing.T) {
	api := statusAPI(t, map[string]string{
		"whoami": `{"hosts":[{"stableNodeID":"n1","approvalLevel":"not-approved","configured":"ready"}]}`,
	})
	daemon := &FakeTailscaleDaemon{ServeJSON: `{"Services":{"svc:whoami":{}}}`}

	statuses, err := serviceStatusesAgainst(api, daemon, "whoami")
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.Contains(t, statuses[0].State, "pending approval")
	assert.Contains(t, statuses[0].State, "autoApprovers.services")
	assert.Contains(t, statuses[0].State, "tag:once")
}

func TestServiceStatusesNotAdvertised(t *testing.T) {
	api := statusAPI(t, map[string]string{
		"whoami": `{"hosts":[{"stableNodeID":"n1","approvalLevel":"approved:auto","configured":"ready"}]}`,
	})
	daemon := &FakeTailscaleDaemon{ServeJSON: `{}`}

	statuses, err := serviceStatusesAgainst(api, daemon, "whoami")
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.Contains(t, statuses[0].State, "not advertised")
	assert.Contains(t, statuses[0].State, "once-docktail container logs")
}

func TestServiceStatusesAdvertisedButUnknownToTailnet(t *testing.T) {
	api := statusAPI(t, map[string]string{"whoami": `{"hosts":[]}`})
	daemon := &FakeTailscaleDaemon{ServeJSON: `{"Services":{"svc:whoami":{}}}`}

	statuses, err := serviceStatusesAgainst(api, daemon, "whoami")
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.Contains(t, statuses[0].State, "not yet in the tailnet")
	assert.Contains(t, statuses[0].State, "once-docktail container logs")
}

func TestServiceStatusesMultipleServicesInOrder(t *testing.T) {
	api := statusAPI(t, map[string]string{
		"once-admin": `{"hosts":[{"stableNodeID":"n1","approvalLevel":"approved:manual","configured":"ready"}]}`,
		"whoami":     `{"hosts":[{"stableNodeID":"n1","approvalLevel":"not-approved","configured":"ready"}]}`,
	})
	daemon := &FakeTailscaleDaemon{ServeJSON: `{"Services":{"svc:once-admin":{},"svc:whoami":{}}}`}

	statuses, err := serviceStatusesAgainst(api, daemon, "once-admin", "whoami")
	require.NoError(t, err)
	require.Len(t, statuses, 2)
	assert.Equal(t, "once-admin", statuses[0].Name)
	assert.Equal(t, "approved", statuses[0].State)
	assert.Equal(t, "whoami", statuses[1].Name)
	assert.Contains(t, statuses[1].State, "pending approval")

	// The credential check comes first, then the per-service devices reads,
	// which must stay byte-compatible with Docktail's listServiceHosts
	// requests: GET /tailnet/-/services/svc:<name>/devices.
	assert.Equal(t, []string{
		tsDevicesPath,
		"/tailnet/-/services/svc:once-admin/devices",
		"/tailnet/-/services/svc:whoami/devices",
	}, api.paths(http.MethodGet))
}

// TestServiceStatusesRejectedCredentialsWithNothingAdvertised covers the
// reported failure: with revoked credentials and a docktail that cannot
// advertise, nothing sends the flow into the cloud, so status used to succeed
// blaming every service on the container logs.
func TestServiceStatusesRejectedCredentialsWithNothingAdvertised(t *testing.T) {
	api := rejectingAPI(t)
	daemon := &FakeTailscaleDaemon{ServeJSON: `{}`}

	statuses, err := serviceStatusesAgainst(api, daemon, "once-admin", "whoami")
	require.Error(t, err, "revoked credentials must not read as services that are merely unadvertised")
	assert.Contains(t, err.Error(), "rejected the OAuth credentials")
	assert.Nil(t, statuses)
}

func TestServiceStatusesRejectedCredentialsReportedBeforeAnyService(t *testing.T) {
	api := rejectingAPI(t)
	daemon := &FakeTailscaleDaemon{ServeJSON: `{"Services":{"svc:whoami":{}}}`}

	_, err := serviceStatusesAgainst(api, daemon, "once-admin", "whoami")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rejected the OAuth credentials")
	assert.Equal(t, []string{tsDevicesPath}, api.paths(http.MethodGet),
		"the credential failure must surface before the per-service listing, not mid-list")
}

func TestServiceStatusesSurfacesCredentialCheckRejection(t *testing.T) {
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodGet, tsDevicesPath, http.StatusForbidden, `{"message":"insufficient scope"}`)
	daemon := &FakeTailscaleDaemon{ServeJSON: `{"Services":{"svc:whoami":{}}}`}

	_, err := serviceStatusesAgainst(api, daemon, "whoami")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
	assert.Contains(t, err.Error(), "insufficient scope")
}

func TestServiceStatusesControlSeamSkipsCloudAPI(t *testing.T) {
	t.Setenv(envControlURL, "http://headscale:8080")
	t.Setenv(envAuthKey, "tskey-auth-abc")

	// The seam cans nothing: a cloud call would fail the test.
	api := newFakeTailscaleAPI(t)
	daemon := &FakeTailscaleDaemon{ServeJSON: `{"Services":{"svc:whoami":{}}}`}

	statuses, err := serviceStatusesAgainst(api, daemon, "whoami", "other")
	require.NoError(t, err)
	require.Len(t, statuses, 2)
	assert.Equal(t, "advertised", statuses[0].State)
	assert.Contains(t, statuses[1].State, "not advertised")
	assert.Zero(t, api.callCount(), "the headscale control seam must not touch the cloud API")
}

func TestServiceStatusesSurfacesDaemonError(t *testing.T) {
	daemon := &FakeTailscaleDaemon{ServeErr: errors.New("no tailscaled socket")}

	_, err := serviceStatusesAgainst(newFakeTailscaleAPI(t), daemon, "whoami")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no tailscaled socket")
}

// Helpers

func serviceStatusesAgainst(api tailscaleAPI, daemon TailscaleDaemon, names ...string) ([]ServiceStatus, error) {
	return serviceStatuses(context.Background(), daemon, api,
		TailscaleSettings{ClientID: "id", ClientSecret: "secret", Tag: "tag:once", DomainSuffix: "tail1234.ts.net"},
		names)
}

// statusAPI is a healthy SaaS tailnet as a status run sees it: the up-front
// credential check answers, then one listServiceHosts response per service.
func statusAPI(t *testing.T, hosts map[string]string) *fakeTailscaleAPI {
	t.Helper()
	api := approvalAPI(t, hosts)
	api.reply(http.MethodGet, tsDevicesPath, http.StatusOK, `{"devices":[]}`)
	return api
}

// rejectingAPI is a tailnet whose OAuth credentials have been revoked: the
// adapter reports the rejection from its lazy token exchange, so every call
// fails the way the first one does.
func rejectingAPI(t *testing.T) *fakeTailscaleAPI {
	t.Helper()
	api := newFakeTailscaleAPI(t)
	api.fail(http.MethodGet, tsDevicesPath, fmt.Errorf("Tailscale rejected the OAuth credentials (401 Unauthorized); check the client ID and secret are correct and not revoked"))
	return api
}
