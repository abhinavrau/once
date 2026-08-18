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

	// The credential check proves the token alone and runs before any
	// per-service call, then the per-service devices reads, which must stay
	// byte-compatible with Docktail's listServiceHosts requests: GET
	// /tailnet/-/services/svc:<name>/devices.
	require.NotEmpty(t, api.calls)
	assert.Equal(t, authenticateMethod, api.calls[0].method)
	assert.Equal(t, []string{
		"/tailnet/-/services/svc:once-admin/devices",
		"/tailnet/-/services/svc:whoami/devices",
	}, api.paths(http.MethodGet), "the credential check must not issue the devices read")
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
	assert.Empty(t, api.paths(http.MethodGet),
		"the credential failure must surface before the per-service listing, not mid-list")
}

// TestServiceStatusesRejectedCredentialsForwardsAuthenticateErrorVerbatim pins
// the wording contract: the check does not construct its own message for a
// rejection, it forwards Authenticate's error exactly as the adapter reports
// it (ADR-0011 keeps status-to-error wording with the adapter's own token
// exchange, since it no longer picks apart a devices-endpoint response).
func TestServiceStatusesRejectedCredentialsForwardsAuthenticateErrorVerbatim(t *testing.T) {
	api := newFakeTailscaleAPI(t)
	rejected := fmt.Errorf("Tailscale rejected the OAuth credentials (401 Unauthorized); check the client ID and secret are correct and not revoked: %w", ErrOAuthCredentialsRejected)
	api.fail(authenticateMethod, "", rejected)
	daemon := &FakeTailscaleDaemon{ServeJSON: `{"Services":{"svc:whoami":{}}}`}

	_, err := serviceStatusesAgainst(api, daemon, "whoami")
	require.Error(t, err)
	assert.Equal(t, rejected, err)
}

// TestServiceStatusesToleratesUnwellTailnetInCredentialCheck pins the boundary
// of the check: it answers "are these credentials still good", so a token
// endpoint that is merely rate-limited or down must not turn a listing that
// would have worked into a hard failure blamed on the operator's credentials.
func TestServiceStatusesToleratesUnwellTailnetInCredentialCheck(t *testing.T) {
	api := newFakeTailscaleAPI(t)
	api.fail(authenticateMethod, "", fmt.Errorf("stub: %w", ErrOAuthTokenEndpointUnavailable))
	api.reply(http.MethodGet, serviceDevicesPath("whoami"),
		http.StatusOK, `{"hosts":[{"stableNodeID":"n1","approvalLevel":"approved:auto","configured":"ready"}]}`)
	daemon := &FakeTailscaleDaemon{ServeJSON: `{"Services":{"svc:whoami":{}}}`}

	statuses, err := serviceStatusesAgainst(api, daemon, "whoami")
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.Equal(t, "approved", statuses[0].State)
}

// TestServiceStatusesUnprovenCredentialCheckAbortsToo pins the other side of
// the boundary: only ErrOAuthTokenEndpointUnavailable is tolerated, so a
// failure that says nothing at all - a network error reaching the token
// endpoint, say - must still abort rather than being silently treated as an
// unwell-but-forgivable tailnet.
func TestServiceStatusesUnprovenCredentialCheckAbortsToo(t *testing.T) {
	api := newFakeTailscaleAPI(t)
	unreachable := errors.New("contacting the Tailscale API: connection refused")
	api.fail(authenticateMethod, "", unreachable)
	daemon := &FakeTailscaleDaemon{ServeJSON: `{"Services":{"svc:whoami":{}}}`}

	_, err := serviceStatusesAgainst(api, daemon, "whoami")
	require.ErrorIs(t, err, unreachable)
	assert.Empty(t, api.paths(http.MethodGet), "an unproven credential check must abort before any per-service call")
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
	return serviceStatuses(context.Background(), daemon, testLogsHint, api,
		TailscaleSettings{ClientID: "id", ClientSecret: "secret", Tag: "tag:once", DomainSuffix: "tail1234.ts.net"},
		names)
}

// statusAPI is a healthy SaaS tailnet as a status run sees it: the up-front
// credential check answers, then one listServiceHosts response per service.
func statusAPI(t *testing.T, hosts map[string]string) *fakeTailscaleAPI {
	t.Helper()
	api := approvalAPI(t, hosts)
	api.reply(authenticateMethod, "", 0, "")
	return api
}

// errRevokedCredentials stands in for the rejection the adapter reports out of
// its lazy token exchange, wrapping ErrOAuthCredentialsRejected as the real
// adapter's tokenExchangeError does, so the credential check's categorization
// sees the same signal these tests assert the failure reaches the caller
// through, without pinning the adapter's exact wording.
var errRevokedCredentials = fmt.Errorf("stub token exchange failure: %w", ErrOAuthCredentialsRejected)

// rejectingAPI is a tailnet whose OAuth credentials have been revoked: the
// adapter fails the token exchange, so the flow's first call never lands.
func rejectingAPI(t *testing.T) *fakeTailscaleAPI {
	t.Helper()
	api := newFakeTailscaleAPI(t)
	api.fail(authenticateMethod, "", errRevokedCredentials)
	return api
}
