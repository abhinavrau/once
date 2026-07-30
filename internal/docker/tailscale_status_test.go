package docker

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func serviceStatusesAgainst(api tailscaleAPI, daemon TailscaleDaemon, names ...string) ([]ServiceStatus, error) {
	return serviceStatuses(context.Background(), daemon, api,
		TailscaleSettings{ClientID: "id", ClientSecret: "secret", Tag: "tag:once", DomainSuffix: "tail1234.ts.net"},
		names)
}

func TestServiceStatusesApproved(t *testing.T) {
	api := approvalAPI(t, map[string]string{
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
	api := approvalAPI(t, map[string]string{
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
	api := approvalAPI(t, map[string]string{
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
	api := approvalAPI(t, map[string]string{"whoami": `{"hosts":[]}`})
	daemon := &FakeTailscaleDaemon{ServeJSON: `{"Services":{"svc:whoami":{}}}`}

	statuses, err := serviceStatusesAgainst(api, daemon, "whoami")
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.Contains(t, statuses[0].State, "not yet in the tailnet")
	assert.Contains(t, statuses[0].State, "once-docktail container logs")
}

func TestServiceStatusesMultipleServicesInOrder(t *testing.T) {
	api := approvalAPI(t, map[string]string{
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

	// The devices reads must stay byte-compatible with Docktail's
	// listServiceHosts requests: GET /tailnet/-/services/svc:<name>/devices.
	assert.Equal(t, []string{
		"/tailnet/-/services/svc:once-admin/devices",
		"/tailnet/-/services/svc:whoami/devices",
	}, api.paths(http.MethodGet))
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
