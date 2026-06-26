package docker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// probeServer stands in for the Tailscale API: it issues a token, mints a key,
// and records the delete so tests can assert the probe cleans up after itself.
type probeServer struct {
	srv        *httptest.Server
	tokenOK    bool
	keyOK      bool
	keyRequest tsKeyRequest
	deletedKey string
}

func newProbeServer(t *testing.T, tokenOK, keyOK bool) *probeServer {
	t.Helper()
	p := &probeServer{tokenOK: tokenOK, keyOK: keyOK}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/oauth/token":
			if !p.tokenOK {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "tskey-api-probe"})
		case r.Method == http.MethodPost && r.URL.Path == "/tailnet/-/keys":
			assert.Equal(t, "Bearer tskey-api-probe", r.Header.Get("Authorization"))
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &p.keyRequest)
			if !p.keyOK {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"message":"requested tags are invalid or not permitted"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "k123", "key": "tskey-auth-xyz"})
		case r.Method == http.MethodDelete:
			p.deletedKey = r.URL.Path
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func TestValidateOAuthCredentialsSuccess(t *testing.T) {
	p := newProbeServer(t, true, true)

	err := validateOAuthCredentials(context.Background(), p.srv.URL,
		TailscaleSettings{ClientID: "id", ClientSecret: "secret", Tag: "tag:once"})
	require.NoError(t, err)

	// Probe must match tsdproxy's CreateKeyRequest shape exactly.
	create := p.keyRequest.Capabilities.Devices.Create
	assert.True(t, create.Ephemeral)
	assert.False(t, create.Reusable)
	assert.True(t, create.Preauthorized)
	assert.Equal(t, []string{"tag:once"}, create.Tags)
	assert.Equal(t, "tsdproxy", p.keyRequest.Description)

	// The minted probe key is deleted so it doesn't accumulate.
	assert.Equal(t, "/tailnet/-/keys/k123", p.deletedKey)
}

func TestValidateOAuthCredentialsUsesNormalizedTag(t *testing.T) {
	p := newProbeServer(t, true, true)

	// EnableTailscale normalizes before probing; a bare "once" must reach the
	// create-key body as "tag:once", not the misleading bare name.
	settings := TailscaleSettings{ClientID: "id", ClientSecret: "secret", Tag: "once,tag:admin"}
	settings.Tag = settings.normalizeTag()
	err := validateOAuthCredentials(context.Background(), p.srv.URL, settings)
	require.NoError(t, err)

	assert.Equal(t, []string{"tag:once", "tag:admin"},
		p.keyRequest.Capabilities.Devices.Create.Tags)
}

func TestValidateOAuthCredentialsRejectsBadCredentials(t *testing.T) {
	p := newProbeServer(t, false, true)

	err := validateOAuthCredentials(context.Background(), p.srv.URL,
		TailscaleSettings{ClientID: "bad", ClientSecret: "bad", Tag: "tag:once"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client ID and secret")
	assert.Empty(t, p.deletedKey, "no key should be minted when credentials are rejected")
}

func TestValidateOAuthCredentialsRejectsBadScopeOrTag(t *testing.T) {
	p := newProbeServer(t, true, false)

	err := validateOAuthCredentials(context.Background(), p.srv.URL,
		TailscaleSettings{ClientID: "id", ClientSecret: "secret", Tag: "tag:wrong"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth_keys scope")
	assert.Contains(t, err.Error(), "tag:wrong")
	assert.Contains(t, err.Error(), "invalid or not permitted")
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
