package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	// tokenPath is the OAuth client-credentials endpoint, the one path the
	// adapter builds itself.
	tokenPath = "/oauth/token"

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

// ErrOAuthCredentialsRejected marks a token exchange failure as a rejection of
// the credentials themselves - the client ID or secret is wrong or revoked -
// as opposed to a throttled or broken token endpoint, which never judged
// them. Authenticate's callers use it to tell the two apart.
var ErrOAuthCredentialsRejected = errors.New("Tailscale rejected the OAuth credentials")

// ErrOAuthTokenEndpointUnavailable marks a token exchange failure that says
// nothing about whether the credentials are good - the token endpoint was
// rate-limited or broken, not judging them. Authenticate's callers use it to
// tell this apart from ErrOAuthCredentialsRejected, the one other failure
// with a settled meaning; anything else (a network failure reaching the
// endpoint at all, an unparseable response) carries neither and is treated
// as unproven rather than merely inconvenient.
var ErrOAuthTokenEndpointUnavailable = errors.New("the Tailscale OAuth token endpoint is unavailable")

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

	// Authenticate proves the credentials alone, via the OAuth token exchange,
	// without issuing any request that touches tailnet data - so a credential
	// check has nothing tailnet-shaped to fail on. A nil result means the
	// credentials are good; ErrOAuthCredentialsRejected marks a rejection of
	// the credentials themselves, and ErrOAuthTokenEndpointUnavailable marks a
	// failure that says nothing about whether they are good. Any other error
	// (a network failure, an unparseable response) is unproven rather than
	// merely inconvenient, and callers that only tolerate the unavailable case
	// should treat it like a rejection.
	Authenticate(ctx context.Context) error
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

func (a *httpTailscaleAPI) Authenticate(ctx context.Context) error {
	_, err := a.accessToken(ctx)
	return err
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+tokenPath, strings.NewReader(form.Encode()))
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
		return "", tokenExchangeError(resp)
	}

	body, err := readBoundedBody(resp.Body, http.MethodPost+" "+tokenPath)
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
// throttled or broken API never judged them.
func tokenExchangeError(resp *http.Response) error {
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%w: Tailscale rate-limited the OAuth token request (%s); the credentials were not rejected - wait a minute and try again", ErrOAuthTokenEndpointUnavailable, resp.Status)
	case resp.StatusCode >= http.StatusInternalServerError:
		return fmt.Errorf("%w: the OAuth token request failed with a server error (%s); the credentials were not rejected - try again shortly", ErrOAuthTokenEndpointUnavailable, resp.Status)
	case resp.StatusCode == http.StatusBadRequest, resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w (%s); check the client ID and secret are correct and not revoked", ErrOAuthCredentialsRejected, resp.Status)
	default:
		return fmt.Errorf("unexpected response from the Tailscale OAuth token endpoint (%s)", resp.Status)
	}
}

// readBoundedBody reads a response body up to the adapter's bound, so no
// Tailscale API response - the token exchange included - can exhaust memory. A
// body that reaches the bound fails naming the request it answered, because
// parsing the truncated bytes would report a bogus syntax error instead.
func readBoundedBody(body io.Reader, request string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, tailscaleAPIMaxResponse+1))
	if err != nil {
		return nil, fmt.Errorf("reading the Tailscale API response to %s: %w", request, err)
	}
	if len(data) > tailscaleAPIMaxResponse {
		return nil, fmt.Errorf("the Tailscale API response to %s is larger than the %d bytes Once will read", request, tailscaleAPIMaxResponse)
	}
	return data, nil
}
