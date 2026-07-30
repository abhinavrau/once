package docker

import (
	"context"
	"errors"
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
	require.ErrorIs(t, err, errRevokedCredentials, "revoked credentials must not read as merely unadvertised services")
	assert.Nil(t, statuses)
}

func TestServiceStatusesRejectedCredentialsReportedBeforeAnyService(t *testing.T) {
	api := rejectingAPI(t)
	daemon := &FakeTailscaleDaemon{ServeJSON: `{"Services":{"svc:whoami":{}}}`}

	_, err := serviceStatusesAgainst(api, daemon, "once-admin", "whoami")
	require.ErrorIs(t, err, errRevokedCredentials)
	assert.Equal(t, []string{tsDevicesPath}, api.paths(http.MethodGet),
		"the credential failure must surface before the per-service listing, not mid-list")
}

func TestServiceStatusesUnauthorizedCredentialCheck(t *testing.T) {
	_, err := serviceStatusesAgainst(credentialCheckStatus(t, http.StatusUnauthorized),
		&FakeTailscaleDaemon{ServeJSON: `{"Services":{"svc:whoami":{}}}`}, "whoami")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rejected the OAuth credentials")
	assert.Contains(t, err.Error(), "not revoked")
}

func TestServiceStatusesForbiddenCredentialCheckNamesTheMissingScope(t *testing.T) {
	_, err := serviceStatusesAgainst(credentialCheckStatus(t, http.StatusForbidden),
		&FakeTailscaleDaemon{ServeJSON: `{"Services":{"svc:whoami":{}}}`}, "whoami")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "devices scope")
	assert.Contains(t, err.Error(), "no access")
}

// TestServiceStatusesToleratesUnwellTailnetInCredentialCheck pins the boundary
// of the check: it answers "are these credentials still good", so a tailnet
// that is merely rate-limited or down must not turn a listing that would have
// worked into a hard failure blamed on the operator's credentials.
func TestServiceStatusesToleratesUnwellTailnetInCredentialCheck(t *testing.T) {
	api := credentialCheckStatus(t, http.StatusTooManyRequests)
	api.reply(http.MethodGet, serviceDevicesPath("whoami"),
		http.StatusOK, `{"hosts":[{"stableNodeID":"n1","approvalLevel":"approved:auto","configured":"ready"}]}`)
	daemon := &FakeTailscaleDaemon{ServeJSON: `{"Services":{"svc:whoami":{}}}`}

	statuses, err := serviceStatusesAgainst(api, daemon, "whoami")
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.Equal(t, "approved", statuses[0].State)
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

// errRevokedCredentials stands in for the rejection the adapter reports out of
// its lazy token exchange, so these tests assert the failure reaches the caller
// without pinning the adapter's wording.
var errRevokedCredentials = errors.New("Tailscale rejected the OAuth credentials")

// rejectingAPI is a tailnet whose OAuth credentials have been revoked: the
// adapter fails the token exchange, so the flow's first call never lands.
func rejectingAPI(t *testing.T) *fakeTailscaleAPI {
	t.Helper()
	api := newFakeTailscaleAPI(t)
	api.fail(http.MethodGet, tsDevicesPath, errRevokedCredentials)
	return api
}

// credentialCheckStatus answers the up-front credential check with one status
// and a message body, as the API refuses a request it did accept a token for.
func credentialCheckStatus(t *testing.T, status int) *fakeTailscaleAPI {
	t.Helper()
	api := newFakeTailscaleAPI(t)
	api.reply(http.MethodGet, tsDevicesPath, status, `{"message":"no access"}`)
	return api
}
