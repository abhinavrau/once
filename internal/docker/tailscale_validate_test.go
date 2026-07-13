package docker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// probeServer stands in for the Tailscale API: it issues a token, mints and
// deletes an auth key, and serves the probe Service endpoints. Each endpoint's
// status is configurable, and requests are recorded so tests can assert the
// probe's request shapes and cleanup. The zero configuration from
// newProbeServer answers every probe step with success.
type probeServer struct {
	srv *httptest.Server

	tokenStatus      int
	keyStatus        int
	keyBody          string
	serviceGetStatus int
	serviceGetBody   string
	servicePutStatus int
	servicePutBody   string
	devicesStatus    int

	keyRequest       tsKeyRequest
	putRequestBody   string
	deletedKey       string
	deletedService   bool
	devicesRequested bool
}

func newProbeServer(t *testing.T) *probeServer {
	t.Helper()
	p := &probeServer{
		tokenStatus:      http.StatusOK,
		keyStatus:        http.StatusOK,
		serviceGetStatus: http.StatusNotFound,
		servicePutStatus: http.StatusOK,
		devicesStatus:    http.StatusOK,
	}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			assert.Equal(t, "Bearer tskey-api-probe", r.Header.Get("Authorization"))
		}
		switch {
		case r.URL.Path == "/oauth/token":
			if p.tokenStatus != http.StatusOK {
				w.WriteHeader(p.tokenStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "tskey-api-probe"})
		case r.Method == http.MethodPost && r.URL.Path == "/tailnet/-/keys":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &p.keyRequest)
			if p.keyStatus != http.StatusOK {
				w.WriteHeader(p.keyStatus)
				_, _ = w.Write([]byte(p.keyBody))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "k123", "key": "tskey-auth-xyz"})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/tailnet/-/keys/"):
			p.deletedKey = r.URL.Path
		case r.Method == http.MethodGet && r.URL.Path == "/tailnet/-/services/svc:once-probe/devices":
			p.devicesRequested = true
			if p.devicesStatus != http.StatusOK {
				w.WriteHeader(p.devicesStatus)
				_, _ = w.Write([]byte(`{"message":"calling actor does not have enough permissions"}`))
				return
			}
			_, _ = w.Write([]byte(`{"hosts":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/tailnet/-/services/svc:once-probe":
			w.WriteHeader(p.serviceGetStatus)
			_, _ = w.Write([]byte(p.serviceGetBody))
		case r.Method == http.MethodPut && r.URL.Path == "/tailnet/-/services/svc:once-probe":
			body, _ := io.ReadAll(r.Body)
			p.putRequestBody = string(body)
			if p.servicePutStatus != http.StatusOK {
				w.WriteHeader(p.servicePutStatus)
				_, _ = w.Write([]byte(p.servicePutBody))
				return
			}
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/tailnet/-/services/svc:once-probe":
			p.deletedService = true
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func validateAgainst(p *probeServer, tag string) error {
	return validateOAuthCredentials(context.Background(), p.srv.URL,
		TailscaleSettings{ClientID: "id", ClientSecret: "secret", Tag: tag})
}

func TestValidateOAuthCredentialsSuccess(t *testing.T) {
	p := newProbeServer(t)

	require.NoError(t, validateAgainst(p, "tag:once"))

	// The probe key must match the sidecar auth key Once mints: single-use,
	// non-ephemeral, preauthorized, tagged.
	create := p.keyRequest.Capabilities.Devices.Create
	assert.False(t, create.Ephemeral)
	assert.False(t, create.Reusable)
	assert.True(t, create.Preauthorized)
	assert.Equal(t, []string{"tag:once"}, create.Tags)
	assert.Equal(t, "once-tailscale", p.keyRequest.Description)
	assert.Equal(t, "/tailnet/-/keys/k123", p.deletedKey)

	// The Service create must be byte-compatible with Docktail's putService
	// payload (json.Marshal of a map: alphabetical keys).
	assert.Equal(t,
		`{"comment":"Once credential probe - safe to delete","name":"svc:once-probe","ports":["tcp:443"],"tags":["tag:once"]}`,
		p.putRequestBody)
	assert.True(t, p.devicesRequested)
	assert.True(t, p.deletedService, "probe Service must be cleaned up")
}

func TestValidateOAuthCredentialsUsesNormalizedTag(t *testing.T) {
	p := newProbeServer(t)

	// EnableTailscale normalizes before probing; a bare "once" must reach the
	// key and Service payloads as "tag:once", not the misleading bare name.
	settings := TailscaleSettings{ClientID: "id", ClientSecret: "secret", Tag: "once,tag:admin"}
	settings.Tag = settings.normalizeTag()
	err := validateOAuthCredentials(context.Background(), p.srv.URL, settings)
	require.NoError(t, err)

	assert.Equal(t, []string{"tag:once", "tag:admin"},
		p.keyRequest.Capabilities.Devices.Create.Tags)
	assert.Contains(t, p.putRequestBody, `"tags":["tag:once","tag:admin"]`)
}

func TestValidateOAuthCredentialsRejectsBadCredentials(t *testing.T) {
	p := newProbeServer(t)
	p.tokenStatus = http.StatusUnauthorized

	err := validateAgainst(p, "tag:once")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client ID and secret")
	assert.Empty(t, p.deletedKey, "no key should be minted when credentials are rejected")
}

func TestValidateOAuthCredentialsMissingAuthKeysScope(t *testing.T) {
	p := newProbeServer(t)
	p.keyStatus = http.StatusForbidden
	p.keyBody = `{"message":"calling actor does not have enough permissions"}`

	err := validateAgainst(p, "tag:once")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth_keys")
	assert.Contains(t, err.Error(), "does not have enough permissions")
	assert.Empty(t, p.putRequestBody, "the Service probe must not run after a scope failure")
}

func TestValidateOAuthCredentialsRejectsUnownedTag(t *testing.T) {
	p := newProbeServer(t)
	p.keyStatus = http.StatusBadRequest
	p.keyBody = `{"message":"requested tags are invalid or not permitted"}`

	err := validateAgainst(p, "tag:wrong")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tag:wrong")
	assert.Contains(t, err.Error(), "tagOwners")
	assert.Contains(t, err.Error(), "invalid or not permitted")
	assert.NotContains(t, err.Error(), "auth_keys", "an unowned tag must not read as a scope problem")
}

func TestValidateOAuthCredentialsMissingServicesScope(t *testing.T) {
	p := newProbeServer(t)
	p.serviceGetStatus = http.StatusForbidden
	p.serviceGetBody = `{"message":"calling actor does not have enough permissions"}`

	err := validateAgainst(p, "tag:once")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Services: Write")
	assert.NotEmpty(t, p.deletedKey, "the auth key probe runs and cleans up first")
}

func TestValidateOAuthCredentialsRejectsUndefinedServiceTag(t *testing.T) {
	p := newProbeServer(t)
	p.servicePutStatus = http.StatusBadRequest
	p.servicePutBody = `{"message":"tag \"tag:once\" is invalid or not permitted"}`

	err := validateAgainst(p, "tag:once")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tag:once")
	assert.Contains(t, err.Error(), "tagOwners")
	assert.False(t, p.deletedService, "a rejected create leaves nothing to delete")
}

func TestValidateOAuthCredentialsMissingDevicesScope(t *testing.T) {
	p := newProbeServer(t)
	p.devicesStatus = http.StatusForbidden

	err := validateAgainst(p, "tag:once")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Devices -> Core")
	assert.True(t, p.deletedService, "probe Service must be cleaned up even when the devices check fails")
}

func TestValidateOAuthCredentialsRefusesForeignServiceName(t *testing.T) {
	p := newProbeServer(t)
	p.serviceGetStatus = http.StatusOK
	p.serviceGetBody = `{"name":"svc:once-probe","comment":"my real service"}`

	err := validateAgainst(p, "tag:once")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
	assert.Empty(t, p.putRequestBody, "a foreign Service must never be overwritten")
	assert.False(t, p.deletedService, "a foreign Service must never be deleted")
}

func TestValidateOAuthCredentialsReusesLeftoverProbeService(t *testing.T) {
	p := newProbeServer(t)
	p.serviceGetStatus = http.StatusOK
	p.serviceGetBody = `{"name":"svc:once-probe","comment":"Once credential probe - safe to delete"}`

	// A leftover from an interrupted probe is ours: overwrite and delete it
	// rather than failing enable forever.
	require.NoError(t, validateAgainst(p, "tag:once"))
	assert.NotEmpty(t, p.putRequestBody)
	assert.True(t, p.deletedService)
}

func TestValidateOAuthCredentialsRequiresTag(t *testing.T) {
	err := validateOAuthCredentials(context.Background(), "http://unused",
		TailscaleSettings{ClientID: "id", ClientSecret: "secret"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tag is required")
}

func TestValidateTailscaleCredentialsSkipsControlSeam(t *testing.T) {
	t.Setenv(envControlURL, "http://headscale:8080")
	t.Setenv(envAuthKey, "tskey-auth-abc")

	// No tag, no reachable API: validation must short-circuit on the control seam.
	err := validateTailscaleCredentials(context.Background(), TailscaleSettings{})
	require.NoError(t, err)
}
