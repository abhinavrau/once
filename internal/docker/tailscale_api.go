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
)

const (
	// tailscaleAPIBaseURL is the production Tailscale SaaS API root - the only
	// place it appears.
	tailscaleAPIBaseURL = "https://api.tailscale.com/api/v2"

	// tailscaleAPIMaxResponse bounds how much of a response body is read, so a
	// broken or hostile API cannot exhaust memory.
	tailscaleAPIMaxResponse = 1 << 16
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
// the rest of its life, so no expiry handling is needed.
type httpTailscaleAPI struct {
	baseURL      string
	clientID     string
	clientSecret string

	token string
}

// newTailscaleAPI returns the adapter for one Tailscale SaaS operation, aimed
// at the real API.
func newTailscaleAPI(settings TailscaleSettings) *httpTailscaleAPI {
	return newTailscaleAPIAt(tailscaleAPIBaseURL, settings)
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

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("contacting the Tailscale API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, tailscaleAPIMaxResponse))
	if err != nil {
		return 0, nil, fmt.Errorf("reading Tailscale API response: %w", err)
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

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("contacting the Tailscale API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Tailscale rejected the OAuth credentials (%s); check the client ID and secret are correct and not revoked", resp.Status)
	}

	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decoding Tailscale OAuth token response: %w", err)
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("Tailscale OAuth token response contained no access token")
	}
	a.token = body.AccessToken
	return a.token, nil
}

// Helpers

// newTailscaleAPIAt builds the adapter against an explicit base URL; only the
// production constructor and the adapter's own tests call it.
func newTailscaleAPIAt(baseURL string, settings TailscaleSettings) *httpTailscaleAPI {
	return &httpTailscaleAPI{
		baseURL:      baseURL,
		clientID:     settings.ClientID,
		clientSecret: settings.ClientSecret,
	}
}
