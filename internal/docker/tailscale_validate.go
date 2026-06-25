package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const tailscaleAPIBaseURL = "https://api.tailscale.com/api/v2"

// tsKeyRequest mirrors the subset of Tailscale's CreateKeyRequest that tsdproxy
// sends, so the probe and tsdproxy's real registration stay in lockstep.
type tsKeyRequest struct {
	Capabilities struct {
		Devices struct {
			Create struct {
				Reusable      bool     `json:"reusable"`
				Ephemeral     bool     `json:"ephemeral"`
				Preauthorized bool     `json:"preauthorized"`
				Tags          []string `json:"tags"`
			} `json:"create"`
		} `json:"devices"`
	} `json:"capabilities"`
	Description string `json:"description"`
}

// validateTailscaleCredentials is the enable-time pre-flight: it proves the
// OAuth credentials work before any container is booted. Skipped on the hidden
// headscale control seam, which uses a pre-minted auth key, not OAuth.
func validateTailscaleCredentials(ctx context.Context, settings TailscaleSettings) error {
	if os.Getenv(envControlURL) != "" && os.Getenv(envAuthKey) != "" {
		return nil
	}
	return validateOAuthCredentials(ctx, tailscaleAPIBaseURL, settings)
}

// validateOAuthCredentials runs a mint-and-delete probe: exchange the client
// credentials for a token, mint an auth key matching the shape tsdproxy mints,
// then delete it. The probe must match tsdproxy's real request, or it would pass
// here and fail at registration (or false-reject working credentials). baseURL
// is parameterised so tests can point it at an httptest server.
func validateOAuthCredentials(ctx context.Context, baseURL string, settings TailscaleSettings) error {
	if settings.Tag == "" {
		return fmt.Errorf("a Tailscale tag is required (e.g. tag:once); tsdproxy cannot mint an OAuth auth key without one")
	}

	token, err := tailscaleOAuthToken(ctx, baseURL, settings.ClientID, settings.ClientSecret)
	if err != nil {
		return err
	}

	keyID, err := createProbeKey(ctx, baseURL, token, settings.Tag)
	if err != nil {
		return err
	}

	// Best-effort cleanup: the credentials are already proven valid, so a failed
	// delete must not fail the enable. ponytail: probe keys also expire on
	// Tailscale's default schedule; the delete just keeps repeated enables from
	// piling up stale keys.
	_ = deleteProbeKey(ctx, baseURL, token, keyID)
	return nil
}

// Helpers

// tailscaleOAuthToken exchanges the OAuth client credentials for an access
// token. A failure here means the client ID or secret is wrong or revoked.
func tailscaleOAuthToken(ctx context.Context, baseURL, clientID, clientSecret string) (string, error) {
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/oauth/token", strings.NewReader(form.Encode()))
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
	return body.AccessToken, nil
}

// createProbeKey mints a tagged auth key matching tsdproxy's CreateKeyRequest
// (see tsdproxy internal/proxyproviders/tailscale: ephemeral from the app label,
// reusable=false, preauthorized=true, tagged). A failure here means the OAuth
// client lacks the auth_keys scope or does not own the requested tag.
func createProbeKey(ctx context.Context, baseURL, token, tag string) (string, error) {
	var reqBody tsKeyRequest
	reqBody.Description = "tsdproxy"
	reqBody.Capabilities.Devices.Create.Ephemeral = true
	reqBody.Capabilities.Devices.Create.Reusable = false
	reqBody.Capabilities.Devices.Create.Preauthorized = true
	reqBody.Capabilities.Devices.Create.Tags = strings.Split(tag, ",")

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/tailnet/-/keys", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("contacting the Tailscale API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Tailscale rejected the probe auth key for tag %q (%s): %s; ensure your OAuth client has the auth_keys scope and owns this tag (Access Controls → tagOwners)", tag, resp.Status, readErrorMessage(resp.Body))
	}

	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decoding Tailscale key response: %w", err)
	}
	return body.ID, nil
}

func deleteProbeKey(ctx context.Context, baseURL, token, keyID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, baseURL+"/tailnet/-/keys/"+url.PathEscape(keyID), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("deleting probe auth key: %s", resp.Status)
	}
	return nil
}

func readErrorMessage(r io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(r, 4096))
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &e) == nil && e.Message != "" {
		return e.Message
	}
	return strings.TrimSpace(string(data))
}
