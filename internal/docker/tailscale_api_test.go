package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestTailscaleAPIRateLimitedTokenExchange(t *testing.T) {
	s := newAPIServer(t)
	s.tokenStatus = http.StatusTooManyRequests
	s.tokenBody = ""

	_, _, err := s.api().Call(context.Background(), http.MethodGet, "/tailnet/-/devices", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rate-limited the OAuth token request (429 Too Many Requests)")
	assert.NotContains(t, err.Error(), "rejected the OAuth credentials")
	assert.Empty(t, s.calls)
}

func TestTailscaleAPIServerErrorTokenExchange(t *testing.T) {
	s := newAPIServer(t)
	s.tokenStatus = http.StatusBadGateway
	s.tokenBody = ""

	_, _, err := s.api().Call(context.Background(), http.MethodGet, "/tailnet/-/devices", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not issue an OAuth token (502 Bad Gateway)")
	assert.Contains(t, err.Error(), "error on Tailscale's side")
	assert.NotContains(t, err.Error(), "rejected the OAuth credentials")
	assert.Empty(t, s.calls)
}

func TestTailscaleAPIUnexpectedTokenStatus(t *testing.T) {
	s := newAPIServer(t)
	s.tokenStatus = http.StatusNotFound
	s.tokenBody = ""

	_, _, err := s.api().Call(context.Background(), http.MethodGet, "/tailnet/-/devices", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected response from the Tailscale OAuth token endpoint (404 Not Found)")
	assert.NotContains(t, err.Error(), "rejected the OAuth credentials")
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

func TestTailscaleAPIRejectsAnOversizedResponse(t *testing.T) {
	s := newAPIServer(t)
	s.callBody = `{"devices":[` + strings.Repeat("x", tailscaleAPIMaxResponse)

	_, _, err := s.api().Call(context.Background(), http.MethodGet, "/tailnet/-/devices", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GET /tailnet/-/devices is larger than")
	assert.NotContains(t, err.Error(), "unexpected end of JSON input")
}

// A tailnet big enough to have crossed the adapter's original 64KiB bound must
// come back whole and parse, rather than failing enable with a JSON error that
// scales in with tailnet size.
func TestTailscaleAPIReadsALargeTailnetDeviceList(t *testing.T) {
	s := newAPIServer(t)
	s.callBody = devicesListJSON(80, "nodekey:host")
	require.Greater(t, len(s.callBody), 1<<16)

	require.NoError(t, validateHostInTailnet(context.Background(), s.api(), "nodekey:host"))
}

func TestTailscaleAPIRejectsAnOversizedTokenResponse(t *testing.T) {
	s := newAPIServer(t)
	s.tokenBody = `{"access_token":"` + strings.Repeat("x", tailscaleAPIMaxResponse)

	_, _, err := s.api().Call(context.Background(), http.MethodGet, "/tailnet/-/devices", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the OAuth token exchange is larger than")
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

// An endpoint that accepts the connection and then never answers must fail the
// call, not hang the enable that made it.
func TestTailscaleAPIFailsWhenTheAPINeverResponds(t *testing.T) {
	api := apiAgainst(stalledServerURL(t))
	api.client.Timeout = 50 * time.Millisecond

	done := make(chan error, 1)
	go func() {
		_, _, err := api.Call(context.Background(), http.MethodGet, "/tailnet/-/devices", nil)
		done <- err
	}()

	select {
	case err := <-done:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "contacting the Tailscale API")
	case <-time.After(10 * time.Second):
		t.Fatal("a stalled Tailscale API hung the call")
	}
}

func TestTailscaleAPIBoundsEveryRequestWithItsOwnClient(t *testing.T) {
	api := newTailscaleAPI(TailscaleSettings{ClientID: "id", ClientSecret: "secret"})
	assert.Equal(t, tailscaleAPIRequestTimeout, api.client.Timeout)
	assert.Nil(t, api.client.Transport, "the transport stays default so ADR-0005 metadata logging still applies")
	assert.Less(t, tailscaleAPIRequestTimeout, serviceVerifyTimeout, "the poll budget, not one call, must govern the verify loop")
}

// Helpers

// apiAgainst builds the production adapter against a test server. Only these
// tests use it: the flows are tested through fakeTailscaleAPI, which is why
// this file holds the last httptest server in the Tailscale tests.
func apiAgainst(baseURL string) *httpTailscaleAPI {
	return newTailscaleAPIAt(baseURL, TailscaleSettings{ClientID: "id", ClientSecret: "secret"})
}

// stalledServerURL serves an endpoint that accepts the connection and holds it
// open without ever responding, until the test ends.
func stalledServerURL(t *testing.T) string {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	return srv.URL
}

// devicesListJSON builds a devices-list response of realistically sized device
// records, the last of which carries nodeKey.
func devicesListJSON(count int, nodeKey string) string {
	devices := make([]map[string]any, 0, count)
	for i := range count {
		devices = append(devices, map[string]any{
			"id":                 fmt.Sprintf("%d", 100000000000000+i),
			"nodeId":             fmt.Sprintf("nodeid-%d", i),
			"nodeKey":            fmt.Sprintf("nodekey:%064d", i),
			"name":               fmt.Sprintf("device-%d.tailnet-abcd.ts.net", i),
			"hostname":           fmt.Sprintf("device-%d", i),
			"user":               "someone@example.com",
			"os":                 "linux",
			"clientVersion":      "1.88.4-t1234abcdef",
			"created":            "2026-01-02T03:04:05Z",
			"lastSeen":           "2026-07-30T03:04:05Z",
			"addresses":          []string{"100.64.0.1", "fd7a:115c:a1e0::1"},
			"tags":               []string{"tag:once"},
			"machineKey":         fmt.Sprintf("mkey:%064d", i),
			"keyExpiryDisabled":  false,
			"updateAvailable":    false,
			"blocksIncomingConn": false,
			"expires":            "2027-01-02T03:04:05Z",
			"authorized":         true,
			"isExternal":         false,
			"advertisedRoutes":   []string{},
			"enabledRoutes":      []string{},
			"clientConnectivity": map[string]any{
				"endpoints":             []string{"192.0.2.10:41641", "[2001:db8::10]:41641"},
				"mappingVariesByDestIP": false,
				"latency": map[string]any{
					"Chicago":  map[string]any{"latencyMs": 12.34},
					"Dallas":   map[string]any{"preferred": true, "latencyMs": 7.65},
					"New York": map[string]any{"latencyMs": 21.09},
				},
				"clientSupports": map[string]bool{
					"hairPinning": false, "ipv6": true, "pcp": false, "pmp": false, "udp": true, "upnp": false,
				},
			},
		})
	}
	devices[count-1]["nodeKey"] = nodeKey

	body, err := json.Marshal(map[string]any{"devices": devices})
	if err != nil {
		panic(err)
	}
	return string(body)
}
