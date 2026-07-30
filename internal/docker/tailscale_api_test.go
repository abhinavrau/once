package docker

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordedRequest is one request the adapter sent to apiServer.
type recordedRequest struct {
	method      string
	path        string
	auth        string
	payload     string
	contentType string
}

// apiServer stands in for the Tailscale SaaS API in the adapter's own tests -
// the OAuth exchange cannot run in CI, so this is where it is covered. It
// serves the token endpoint and one echo endpoint, recording every request so
// the adapter's behavior (exchange, caching, bearer header) can be asserted.
type apiServer struct {
	srv *httptest.Server

	tokenStatus   int
	tokenBody     string
	tokenRequests int
	tokenForm     map[string]string
	tokenType     string

	callStatus int
	callBody   string
	calls      []recordedRequest
}

func newAPIServer(t *testing.T) *apiServer {
	t.Helper()
	s := &apiServer{
		tokenStatus: http.StatusOK,
		tokenBody:   `{"access_token":"tskey-api-test"}`,
		callStatus:  http.StatusOK,
		callBody:    `{"devices":[]}`,
	}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			s.tokenRequests++
			require.NoError(t, r.ParseForm())
			s.tokenForm = map[string]string{}
			for key := range r.Form {
				s.tokenForm[key] = r.Form.Get(key)
			}
			s.tokenType = r.Header.Get("Content-Type")
			w.WriteHeader(s.tokenStatus)
			_, _ = w.Write([]byte(s.tokenBody))
			return
		}

		payload, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		s.calls = append(s.calls, recordedRequest{
			method:      r.Method,
			path:        r.URL.Path,
			auth:        r.Header.Get("Authorization"),
			payload:     string(payload),
			contentType: r.Header.Get("Content-Type"),
		})
		w.WriteHeader(s.callStatus)
		_, _ = w.Write([]byte(s.callBody))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *apiServer) api() *httpTailscaleAPI {
	return apiAgainst(s.srv.URL)
}

func TestTailscaleAPIExchangesTokenAndSendsBearer(t *testing.T) {
	s := newAPIServer(t)

	status, body, err := s.api().Call(context.Background(), http.MethodGet, "/tailnet/-/devices", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, `{"devices":[]}`, string(body))

	assert.Equal(t, 1, s.tokenRequests)
	assert.Equal(t, map[string]string{
		"grant_type":    "client_credentials",
		"client_id":     "id",
		"client_secret": "secret",
	}, s.tokenForm)
	assert.Equal(t, "application/x-www-form-urlencoded", s.tokenType)

	require.Len(t, s.calls, 1)
	assert.Equal(t, recordedRequest{
		method: http.MethodGet,
		path:   "/tailnet/-/devices",
		auth:   "Bearer tskey-api-test",
	}, s.calls[0])
}

func TestTailscaleAPISendsJSONPayload(t *testing.T) {
	s := newAPIServer(t)

	_, _, err := s.api().Call(context.Background(), http.MethodPut, "/tailnet/-/services/svc:x", []byte(`{"name":"svc:x"}`))
	require.NoError(t, err)

	require.Len(t, s.calls, 1)
	assert.Equal(t, `{"name":"svc:x"}`, s.calls[0].payload)
	assert.Equal(t, "application/json", s.calls[0].contentType)
}

func TestTailscaleAPIBodylessCallSendsNoContentType(t *testing.T) {
	s := newAPIServer(t)

	_, _, err := s.api().Call(context.Background(), http.MethodDelete, "/tailnet/-/keys/k123", nil)
	require.NoError(t, err)

	require.Len(t, s.calls, 1)
	assert.Empty(t, s.calls[0].payload)
	assert.Empty(t, s.calls[0].contentType)
}

func TestTailscaleAPIRejectedCredentials(t *testing.T) {
	s := newAPIServer(t)
	s.tokenStatus = http.StatusUnauthorized
	s.tokenBody = ""

	_, _, err := s.api().Call(context.Background(), http.MethodGet, "/tailnet/-/devices", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Tailscale rejected the OAuth credentials (401 Unauthorized)")
	assert.Contains(t, err.Error(), "check the client ID and secret are correct and not revoked")
	assert.Empty(t, s.calls, "a rejected exchange must not send the request")
}

func TestTailscaleAPITokenResponseWithoutAccessToken(t *testing.T) {
	s := newAPIServer(t)
	s.tokenBody = `{"token_type":"bearer"}`

	_, _, err := s.api().Call(context.Background(), http.MethodGet, "/tailnet/-/devices", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no access token")
	assert.Empty(t, s.calls)
}

func TestTailscaleAPIUndecodableTokenResponse(t *testing.T) {
	s := newAPIServer(t)
	s.tokenBody = "not json"

	_, _, err := s.api().Call(context.Background(), http.MethodGet, "/tailnet/-/devices", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decoding Tailscale OAuth token response")
}

func TestTailscaleAPIReusesTokenAcrossCalls(t *testing.T) {
	s := newAPIServer(t)
	api := s.api()

	for range 3 {
		_, _, err := api.Call(context.Background(), http.MethodGet, "/tailnet/-/devices", nil)
		require.NoError(t, err)
	}

	assert.Equal(t, 1, s.tokenRequests, "the exchange happens at most once per adapter instance")
	require.Len(t, s.calls, 3)
	for _, call := range s.calls {
		assert.Equal(t, "Bearer tskey-api-test", call.auth)
	}
}

func TestTailscaleAPIBoundsResponseBody(t *testing.T) {
	s := newAPIServer(t)
	s.callBody = strings.Repeat("x", tailscaleAPIMaxResponse+4096)

	_, body, err := s.api().Call(context.Background(), http.MethodGet, "/tailnet/-/devices", nil)
	require.NoError(t, err)
	assert.Len(t, body, tailscaleAPIMaxResponse)
}

func TestTailscaleAPIBoundsTokenResponse(t *testing.T) {
	s := newAPIServer(t)
	// The access token sits past the bound, so only an unbounded read reaches it.
	s.tokenBody = `{"padding":"` + strings.Repeat("x", tailscaleAPIMaxResponse) + `","access_token":"tskey-api-test"}`

	_, _, err := s.api().Call(context.Background(), http.MethodGet, "/tailnet/-/devices", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decoding Tailscale OAuth token response")
	assert.Empty(t, s.calls)
}

func TestTailscaleAPIWrapsExchangeNetworkError(t *testing.T) {
	_, _, err := apiAgainst("http://127.0.0.1:1").Call(context.Background(), http.MethodGet, "/tailnet/-/devices", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contacting the Tailscale API")
}

func TestTailscaleAPIWrapsRequestNetworkError(t *testing.T) {
	s := newAPIServer(t)
	api := s.api()

	_, _, err := api.Call(context.Background(), http.MethodGet, "/tailnet/-/devices", nil)
	require.NoError(t, err)

	// The token is cached now, so the next call goes straight to a dead server.
	s.srv.Close()
	_, _, err = api.Call(context.Background(), http.MethodGet, "/tailnet/-/devices", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contacting the Tailscale API")
}

// The adapter must stay silent: verbose logging of its requests comes solely
// from the metadata-only transport (ADR-0005), which never sees bodies.
func TestTailscaleAPILogsNothing(t *testing.T) {
	var logged bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug})))

	s := newAPIServer(t)
	s.callStatus = http.StatusForbidden
	s.callBody = `{"message":"nope"}`

	_, _, err := s.api().Call(context.Background(), http.MethodPut, "/tailnet/-/services/svc:x", []byte(`{"secret":"payload"}`))
	require.NoError(t, err)
	assert.Empty(t, logged.String())
}

// Helpers

// apiAgainst builds the production adapter against a test server. Only these
// tests use it: the flows are tested through fakeTailscaleAPI, which is why
// this file holds the last httptest server in the Tailscale tests.
func apiAgainst(baseURL string) *httpTailscaleAPI {
	return newTailscaleAPIAt(baseURL, TailscaleSettings{ClientID: "id", ClientSecret: "secret"})
}
