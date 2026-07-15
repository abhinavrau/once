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

// keysServer stands in for the Tailscale API's OAuth-client minting: it issues
// a token and answers POST/DELETE on the keys endpoint. Statuses are
// configurable and requests recorded so tests can assert the exact bytes sent.
type keysServer struct {
	srv *httptest.Server

	tokenStatus int
	mintStatus  int
	mintBody    string

	mintRequestBody string
	deletedKey      string
}

func newKeysServer(t *testing.T) *keysServer {
	t.Helper()
	k := &keysServer{tokenStatus: http.StatusOK, mintStatus: http.StatusOK}
	k.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			assert.Equal(t, "Bearer tskey-api-bootstrap", r.Header.Get("Authorization"))
		}
		switch {
		case r.URL.Path == "/oauth/token":
			if k.tokenStatus != http.StatusOK {
				w.WriteHeader(k.tokenStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "tskey-api-bootstrap"})
		case r.Method == http.MethodPost && r.URL.Path == "/tailnet/-/keys":
			body, _ := io.ReadAll(r.Body)
			k.mintRequestBody = string(body)
			if k.mintStatus != http.StatusOK {
				w.WriteHeader(k.mintStatus)
				_, _ = w.Write([]byte(k.mintBody))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "kclient123", "key": "tskey-client-secret"})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/tailnet/-/keys/"):
			k.deletedKey = r.URL.Path
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(k.srv.Close)
	return k
}

func bootstrapAgainst(t *testing.T, k *keysServer) *TailscaleBootstrap {
	t.Helper()
	b, err := newTailscaleBootstrap(context.Background(), k.srv.URL, "boot-id", "boot-secret")
	require.NoError(t, err)
	return b
}

func TestMintRuntimeClient(t *testing.T) {
	k := newKeysServer(t)
	b := bootstrapAgainst(t, k)

	clientID, clientSecret, err := b.MintRuntimeClient(context.Background(), "once")

	require.NoError(t, err)
	assert.Equal(t, "kclient123", clientID)
	assert.Equal(t, "tskey-client-secret", clientSecret)
	// Byte-compatible with the documented keys API shape (tailscale-client-go-v2
	// CreateOAuthClient), with the tag normalized and the minimal runtime scopes.
	assert.Equal(t,
		`{"keyType":"client","scopes":["auth_keys","devices:core","services"],"tags":["tag:once"],"description":"once-docktail"}`,
		k.mintRequestBody)
}

func TestMintPolicyClient(t *testing.T) {
	k := newKeysServer(t)
	b := bootstrapAgainst(t, k)

	clientID, clientSecret, err := b.MintPolicyClient(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "kclient123", clientID)
	assert.Equal(t, "tskey-client-secret", clientSecret)
	// policy_file needs no tags, so the tags key is absent entirely.
	assert.Equal(t,
		`{"keyType":"client","scopes":["policy_file"],"description":"once-policy"}`,
		k.mintRequestBody)
}

func TestBootstrapRejectedCredentials(t *testing.T) {
	k := newKeysServer(t)
	k.tokenStatus = http.StatusUnauthorized

	_, err := newTailscaleBootstrap(context.Background(), k.srv.URL, "boot-id", "boot-secret")

	assert.ErrorContains(t, err, "rejected the OAuth credentials")
}

func TestMintMissingOAuthKeysScope(t *testing.T) {
	k := newKeysServer(t)
	k.mintStatus = http.StatusForbidden
	k.mintBody = `{"message":"calling actor does not have enough permissions"}`
	b := bootstrapAgainst(t, k)

	_, _, err := b.MintRuntimeClient(context.Background(), "tag:once")

	assert.ErrorContains(t, err, "missing the oauth_keys scope")
	assert.ErrorContains(t, err, "calling actor does not have enough permissions")
}

func TestMintRejected(t *testing.T) {
	k := newKeysServer(t)
	k.mintStatus = http.StatusBadRequest
	k.mintBody = `{"message":"requested tags [tag:once] are invalid or not permitted"}`
	b := bootstrapAgainst(t, k)

	_, _, err := b.MintRuntimeClient(context.Background(), "tag:once")

	assert.ErrorContains(t, err, "refused to mint the once-docktail OAuth client for tags tag:once")
	assert.ErrorContains(t, err, "invalid or not permitted")
	assert.ErrorContains(t, err, "tagOwners")
}

func TestDeleteClient(t *testing.T) {
	k := newKeysServer(t)
	b := bootstrapAgainst(t, k)

	require.NoError(t, b.DeleteClient(context.Background(), "kclient123"))
	assert.Equal(t, "/tailnet/-/keys/kclient123", k.deletedKey)
}
