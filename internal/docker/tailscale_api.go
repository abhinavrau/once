package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// tailscaleAPIBaseURL is the production Tailscale SaaS API root - the only
	// place it appears.
	tailscaleAPIBaseURL = "https://api.tailscale.com/api/v2"

	// tailscaleAPIMaxResponse bounds how much of a response body is read, so a
	// broken or hostile API cannot exhaust memory. It sits far above any real
	// response - the largest, a full tailnet device list, runs roughly a
	// kilobyte per device - and a body that reaches it is reported as too
	// large rather than parsed from truncated bytes.
	tailscaleAPIMaxResponse = 8 << 20

	// tailscaleAPIRequestTimeout bounds one request, so an endpoint that
	// accepts the connection and then never answers (captive portal,
	// blackholed egress) cannot hang an enable forever. It is deliberately
	// shorter than the poll budgets that drive repeated calls, so those
	// budgets still govern their loops.
	tailscaleAPIRequestTimeout = 30 * time.Second
)

// tailscaleAPI is Once's seam to the Tailscale SaaS control-plane HTTP API
// (ADR-0011): one authenticated call, whose implementation owns the base URL,
// the OAuth exchange, the bearer header, and the bounded response read. Paths,
// payloads, and status-to-error wording stay with the callers, because several
// requests must remain byte-compatible with what docktail sends and the
// wording is flow-specific.
type tailscaleAPI interface {
	// Call sends an authenticated request to path (rooted at the API base, e.g.
	// "/tailnet/-/keys") and returns the response status and body. A nil payload
	// sends no request body.
	Call(ctx context.Context, method, path string, payload []byte) (int, []byte, error)
}

// httpTailscaleAPI is the production tailscaleAPI: the real Tailscale API over
// HTTP. One instance serves one operation - it exchanges the OAuth client
// credentials for an access token on its first call and reuses that token for
// the rest of its life, so no expiry handling is needed. An operation's calls
// are sequential, so the cached token needs no locking.
type httpTailscaleAPI struct {
	baseURL      string
	clientID     string
	clientSecret string
	client       *http.Client

	token string
}

// newTailscaleAPI returns the adapter for one Tailscale SaaS operation, aimed
// at the real API.
func newTailscaleAPI(settings TailscaleSettings) *httpTailscaleAPI {
	return newTailscaleAPIAt(tailscaleAPIBaseURL, settings)
}

// newTailscaleAPIAt builds the adapter against an explicit base URL; only the
// production constructor and the adapter's own tests call it. The client is the
// adapter's own - http.DefaultClient has no timeout - but its transport is left
// nil so requests still go through http.DefaultTransport, which main wraps in
// the metadata-only logging RoundTripper (ADR-0005).
func newTailscaleAPIAt(baseURL string, settings TailscaleSettings) *httpTailscaleAPI {
	return &httpTailscaleAPI{
		baseURL:      baseURL,
		clientID:     settings.ClientID,
		clientSecret: settings.ClientSecret,
		client:       &http.Client{Timeout: tailscaleAPIRequestTimeout},
	}
}

func (a *httpTailscaleAPI) Call(ctx context.Context, method, path string, payload []byte) (int, []byte, error) {
	token, err := a.accessToken(ctx)
	if err != nil {
		return 0, nil, err
	}

	var reqBody io.Reader
	if payload != nil {
		reqBody = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, reqBody)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("contacting the Tailscale API: %w", err)
	}
	defer resp.Body.Close()

	body, err := readBoundedBody(resp.Body, method+" "+path)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

// Private

// accessToken returns the instance's access token, exchanging the OAuth client
// credentials for one on first use.
func (a *httpTailscaleAPI) accessToken(ctx context.Context) (string, error) {
	if a.token != "" {
		return a.token, nil
	}

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {a.clientID},
		"client_secret": {a.clientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("contacting the Tailscale API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", tokenExchangeError(resp.StatusCode, resp.Status)
	}

	body, err := readBoundedBody(resp.Body, "the OAuth token exchange")
	if err != nil {
		return "", err
	}

	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &token); err != nil {
		return "", fmt.Errorf("decoding Tailscale OAuth token response: %w", err)
	}
	if token.AccessToken == "" {
		return "", fmt.Errorf("Tailscale OAuth token response contained no access token")
	}
	a.token = token.AccessToken
	return a.token, nil
}

// Helpers

// tokenExchangeError turns a non-200 from the OAuth token endpoint into an
// error that names the real problem. Only a refusal of the credentials
// themselves sends the user off to check their client ID and secret: a
// throttled or broken API means the credentials were never judged.
func tokenExchangeError(code int, status string) error {
	switch {
	case code == http.StatusTooManyRequests:
		return fmt.Errorf("Tailscale rate-limited the OAuth token request (%s); wait a minute and try again - the credentials were not rejected", status)
	case code >= http.StatusInternalServerError:
		return fmt.Errorf("the Tailscale API could not issue an OAuth token (%s); this is an error on Tailscale's side, try again shortly", status)
	case code == http.StatusBadRequest, code == http.StatusUnauthorized, code == http.StatusForbidden:
		return fmt.Errorf("Tailscale rejected the OAuth credentials (%s); check the client ID and secret are correct and not revoked", status)
	default:
		return fmt.Errorf("unexpected response from the Tailscale OAuth token endpoint (%s)", status)
	}
}

// readBoundedBody reads a response body up to the adapter's bound, so no
// Tailscale API response - token exchange included - can exhaust memory. A body
// that reaches the bound fails naming what was being read, because parsing the
// truncated bytes would report a bogus syntax error instead.
func readBoundedBody(body io.Reader, what string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, tailscaleAPIMaxResponse+1))
	if err != nil {
		return nil, fmt.Errorf("reading the Tailscale API response to %s: %w", what, err)
	}
	if len(data) > tailscaleAPIMaxResponse {
		return nil, fmt.Errorf("the Tailscale API response to %s is larger than the %d bytes Once will read", what, tailscaleAPIMaxResponse)
	}
	return data, nil
}
