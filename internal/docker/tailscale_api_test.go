package docker

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	rejected := tokenStatusError(t, http.StatusUnauthorized)
	assert.Contains(t, rejected, "Tailscale rejected the OAuth credentials (401 Unauthorized)")
	assert.Contains(t, rejected, "check the client ID and secret are correct and not revoked")
}

// A throttled or broken token endpoint never judged the credentials, so it must
// not send the user off to rotate a working secret.
func TestTailscaleAPIThrottledOrBrokenTokenExchange(t *testing.T) {
	rateLimited := tokenStatusError(t, http.StatusTooManyRequests)
	assert.Contains(t, rateLimited, "rate-limited the OAuth token request (429 Too Many Requests)")
	assert.NotContains(t, rateLimited, "rejected the OAuth credentials")

	serverError := tokenStatusError(t, http.StatusBadGateway)
	assert.Contains(t, serverError, "server error (502 Bad Gateway)")
	assert.NotContains(t, serverError, "rejected the OAuth credentials")

	unexpected := tokenStatusError(t, http.StatusNotFound)
	assert.Contains(t, unexpected, "unexpected response from the Tailscale OAuth token endpoint (404 Not Found)")
	assert.NotContains(t, unexpected, "rejected the OAuth credentials")
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
	assert.Contains(t, err.Error(), "POST /oauth/token is larger than")
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
// call, not hang the enable that made it - on either leg, the token exchange
// (a captive portal swallowing the first contact) or the request it authorizes.
func TestTailscaleAPIFailsWhenTheTokenExchangeNeverResponds(t *testing.T) {
	assertStalledCallFails(t, stalledAPIServer(t, false))
}

func TestTailscaleAPIFailsWhenARequestNeverResponds(t *testing.T) {
	assertStalledCallFails(t, stalledAPIServer(t, true))
}

func TestTailscaleAPIBoundsEveryRequest(t *testing.T) {
	api := newTailscaleAPI(TailscaleSettings{ClientID: "id", ClientSecret: "secret"})
	assert.Equal(t, tailscaleAPIRequestTimeout, api.client.Timeout)
	assert.Less(t, tailscaleAPIRequestTimeout, serviceVerifyTimeout, "the poll budget, not one call, must govern the verify loop")
}

// The adapter's own client must still ride http.DefaultTransport, which main
// wraps in the metadata-only logging RoundTripper (ADR-0005).
func TestTailscaleAPIRequestsRideTheDefaultTransport(t *testing.T) {
	s := newAPIServer(t)
	wrapped := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = wrapped })
	tripped := 0
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		tripped++
		return wrapped.RoundTrip(req)
	})

	_, _, err := s.api().Call(context.Background(), http.MethodGet, "/tailnet/-/devices", nil)
	require.NoError(t, err)
	assert.Equal(t, 2, tripped, "the token exchange and the request both go through the default transport")
}

// Helpers

// apiAgainst builds the production adapter against a test server. Only these
// tests use it: the flows are tested through fakeTailscaleAPI, which is why
// this file holds the last httptest server in the Tailscale tests.
func apiAgainst(baseURL string) *httpTailscaleAPI {
	return newTailscaleAPIAt(baseURL, TailscaleSettings{ClientID: "id", ClientSecret: "secret"})
}

// tokenStatusError returns the error the adapter reports when the OAuth token
// endpoint answers with status.
func tokenStatusError(t *testing.T, status int) string {
	t.Helper()
	s := newAPIServer(t)
	s.tokenStatus = status
	s.tokenBody = ""

	_, _, err := s.api().Call(context.Background(), http.MethodGet, "/tailnet/-/devices", nil)
	require.Error(t, err)
	assert.Empty(t, s.calls, "a failed exchange must not send the request")
	return err.Error()
}

// stalledAPIServer serves an endpoint that accepts the connection and holds it
// open without ever responding, until the test ends. With serveToken the OAuth
// exchange succeeds, so the stall lands on the request it authorizes instead.
func stalledAPIServer(t *testing.T, serveToken bool) string {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveToken && r.URL.Path == tokenPath {
			_, _ = w.Write([]byte(`{"access_token":"tskey-api-test"}`))
			return
		}
		<-release
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	return srv.URL
}

// assertStalledCallFails fails the test unless a call against a stalled server
// returns rather than hanging.
func assertStalledCallFails(t *testing.T, baseURL string) {
	t.Helper()
	api := apiAgainst(baseURL)
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

// devicesListJSON builds a devices-list response of count device records, each
// the shape and size of a real one, the last carrying hostKey. Size is the
// point: how many devices fit in a given number of bytes is what put real
// tailnets over the adapter's original bound.
func devicesListJSON(count int, hostKey string) string {
	const record = `{"addresses":["100.64.0.$N","fd7a:115c:a1e0:ab12:4843:cd96:6249:$N"],` +
		`"id":"92233720368547750$N","nodeId":"n0$NCNTRL","user":"someone@example.com",` +
		`"name":"device-$N.tailnet-abcd.ts.net","hostname":"device-$N","clientVersion":"1.88.4-t1234abcdef",` +
		`"updateAvailable":false,"os":"linux","created":"2026-01-02T03:04:05Z","lastSeen":"2026-07-30T03:04:05Z",` +
		`"keyExpiryDisabled":false,"expires":"2027-01-02T03:04:05Z","authorized":true,"isExternal":false,` +
		`"machineKey":"mkey:5a7f3b2c9e1d4f6a8b0c2d4e6f8a0b1c3d5e7f9a1b3c5d7e9f1a3b5c7d9e0$N",` +
		`"nodeKey":"$K","blocksIncomingConnections":false,"enabledRoutes":[],"advertisedRoutes":[],` +
		`"clientConnectivity":{"endpoints":["192.0.2.$N:41641","[2001:db8::$N]:41641"],` +
		`"mappingVariesByDestIP":false,"latency":{"Chicago":{"latencyMs":12.34},` +
		`"Dallas":{"preferred":true,"latencyMs":7.65},"New York":{"latencyMs":21.09}},` +
		`"clientSupports":{"hairPinning":false,"ipv6":true,"pcp":false,"pmp":false,"udp":true,"upnp":false}},` +
		`"tags":["tag:once"],"tailnetLockError":"","tailnetLockKey":""}`

	device := func(i int, nodeKey string) string {
		return strings.NewReplacer("$N", strconv.Itoa(i), "$K", nodeKey).Replace(record)
	}
	devices := make([]string, count)
	for i := range devices {
		devices[i] = device(i, "nodekey:9f1a3b5c7d9e1f3a5b7c9d1e3f5a7b9c1d3e5f7a9b1c3d5e7f9a1b3c5d7e0"+strconv.Itoa(i))
	}
	devices[count-1] = device(count-1, hostKey)
	return `{"devices":[` + strings.Join(devices, ",") + `]}`
}

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
