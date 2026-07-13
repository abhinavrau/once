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

const (
	tailscaleAPIBaseURL = "https://api.tailscale.com/api/v2"

	// probeServiceName is the throwaway Service definition created and deleted by
	// the enable-time probe. probeServiceComment marks it as Once's, so a leftover
	// from an interrupted probe is recognized and reused instead of blocking enable.
	probeServiceName    = "svc:once-probe"
	probeServiceComment = "Once credential probe - safe to delete"
)

// tsKeyRequest mirrors the subset of Tailscale's CreateKeyRequest Once uses to
// mint the once-tailscale sidecar's auth key, so the probe and the real sidecar
// boot stay in lockstep.
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

// validateOAuthCredentials proves the OAuth client can do everything Docktail
// and the Once-managed sidecar will need, using the same request shapes they
// send: a token exchange, a mint-and-delete of a sidecar-shaped auth key
// (auth_keys scope, tag ownership), and a create-inspect-delete of a throwaway
// Service definition (services and devices scopes, tag definition). Each
// failure mode gets its own actionable error. baseURL is parameterised so
// tests can point it at an httptest server.
func validateOAuthCredentials(ctx context.Context, baseURL string, settings TailscaleSettings) error {
	if settings.Tag == "" {
		return fmt.Errorf("a Tailscale tag is required (e.g. tag:once); the sidecar auth key and Service definitions cannot be created without one")
	}

	token, err := tailscaleOAuthToken(ctx, baseURL, settings.ClientID, settings.ClientSecret)
	if err != nil {
		return err
	}

	keyID, _, err := createTailscaleAuthKey(ctx, baseURL, token, settings.Tag)
	if err != nil {
		return err
	}

	// Best-effort cleanup: the auth_keys scope and tag are already proven, so a
	// failed delete must not fail the enable. ponytail: probe keys also expire on
	// Tailscale's default schedule; the delete just keeps repeated enables from
	// piling up stale keys.
	_ = deleteProbeKey(ctx, baseURL, token, keyID)

	return probeServiceDefinition(ctx, baseURL, token, settings.Tag)
}

// mintSidecarAuthKey mints the tagged auth key the once-tailscale sidecar
// registers with. It shares createTailscaleAuthKey with the enable-time probe,
// so the key the sidecar boots with always has exactly the shape the probe
// proved could be minted.
func mintSidecarAuthKey(ctx context.Context, baseURL string, settings TailscaleSettings) (string, error) {
	token, err := tailscaleOAuthToken(ctx, baseURL, settings.ClientID, settings.ClientSecret)
	if err != nil {
		return "", err
	}
	_, key, err := createTailscaleAuthKey(ctx, baseURL, token, settings.Tag)
	return key, err
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

// createTailscaleAuthKey mints a tagged auth key of the shape Once uses for
// the once-tailscale sidecar's TS_AUTHKEY: single-use, non-ephemeral (the
// sidecar keeps its node state in a volume), preauthorized, tagged. A 403
// means the OAuth client lacks the auth_keys scope; any other rejection means
// it does not own the requested tag.
func createTailscaleAuthKey(ctx context.Context, baseURL, token, tag string) (id, key string, err error) {
	var reqBody tsKeyRequest
	reqBody.Description = "once-tailscale"
	reqBody.Capabilities.Devices.Create.Preauthorized = true
	reqBody.Capabilities.Devices.Create.Tags = strings.Split(tag, ",")

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", err
	}

	status, body, err := tailscaleAPICall(ctx, http.MethodPost, baseURL+"/tailnet/-/keys", token, payload)
	if err != nil {
		return "", "", err
	}
	switch {
	case status == http.StatusForbidden:
		return "", "", fmt.Errorf("the OAuth client is missing the auth_keys scope (Keys -> Auth Keys: Write), needed to mint the Tailscale sidecar's auth key (HTTP %d): %s", status, errorMessage(body))
	case status != http.StatusOK:
		return "", "", fmt.Errorf("Tailscale refused to mint an auth key for tag %q (HTTP %d): %s; ensure the OAuth client is tagged with this tag or owns it via tagOwners (Access Controls -> Tags)", tag, status, errorMessage(body))
	}

	var respBody struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal(body, &respBody); err != nil {
		return "", "", fmt.Errorf("decoding Tailscale key response: %w", err)
	}
	return respBody.ID, respBody.Key, nil
}

func deleteProbeKey(ctx context.Context, baseURL, token, keyID string) error {
	status, _, err := tailscaleAPICall(ctx, http.MethodDelete, baseURL+"/tailnet/-/keys/"+url.PathEscape(keyID), token, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("deleting probe auth key: HTTP %d", status)
	}
	return nil
}

// probeServiceDefinition proves the services scope (General -> Services: Write)
// and the devices scope (Devices -> Core) with the request shapes Docktail
// sends at runtime: check whether the Service exists (SyncServiceDefinition),
// create it (putService), list its advertising devices (listServiceHosts), and
// delete it (deleteService). The create is also where an undefined tag surfaces.
// The "-" tailnet path segment matches Docktail, which defaults
// TAILSCALE_TAILNET to "-" when Once leaves it unset.
func probeServiceDefinition(ctx context.Context, baseURL, token, tag string) error {
	svcURL := baseURL + "/tailnet/-/services/" + probeServiceName

	status, body, err := tailscaleAPICall(ctx, http.MethodGet, svcURL, token, nil)
	if err != nil {
		return err
	}
	switch status {
	case http.StatusNotFound:
		// The name is free, as expected.
	case http.StatusOK:
		var existing struct {
			Comment string `json:"comment"`
		}
		if json.Unmarshal(body, &existing) != nil || existing.Comment != probeServiceComment {
			return fmt.Errorf("a Tailscale Service named %s already exists in the tailnet; Once needs that name free for its credential probe, so remove or rename that Service and re-run", probeServiceName)
		}
		// A leftover from an interrupted probe: safe to overwrite and delete.
	case http.StatusForbidden:
		return servicesScopeError(status, body)
	default:
		return fmt.Errorf("checking for an existing %s Service (HTTP %d): %s", probeServiceName, status, errorMessage(body))
	}

	// Byte-compatible with Docktail's putService create payload.
	payload, err := json.Marshal(map[string]any{
		"name":    probeServiceName,
		"tags":    strings.Split(tag, ","),
		"ports":   []string{"tcp:443"},
		"comment": probeServiceComment,
	})
	if err != nil {
		return err
	}
	status, body, err = tailscaleAPICall(ctx, http.MethodPut, svcURL, token, payload)
	if err != nil {
		return err
	}
	switch {
	case status == http.StatusForbidden:
		return servicesScopeError(status, body)
	case status != http.StatusOK:
		return fmt.Errorf("Tailscale rejected the probe Service definition for tag %q (HTTP %d): %s; ensure the tag is defined in your tailnet policy (Access Controls -> tagOwners)", tag, status, errorMessage(body))
	}

	// The Service now exists; clean it up whether or not the devices check passes.
	devErr := probeServiceDevices(ctx, svcURL, token)
	_ = deleteProbeService(ctx, svcURL, token)
	return devErr
}

func probeServiceDevices(ctx context.Context, svcURL, token string) error {
	status, body, err := tailscaleAPICall(ctx, http.MethodGet, svcURL+"/devices", token, nil)
	if err != nil {
		return err
	}
	switch {
	case status == http.StatusForbidden:
		return fmt.Errorf("the OAuth client is missing the devices scope (Devices -> Core: Write), needed to check which hosts advertise a Service (HTTP %d): %s", status, errorMessage(body))
	case status != http.StatusOK:
		return fmt.Errorf("listing the probe Service's devices (HTTP %d): %s", status, errorMessage(body))
	}
	return nil
}

func deleteProbeService(ctx context.Context, svcURL, token string) error {
	status, body, err := tailscaleAPICall(ctx, http.MethodDelete, svcURL, token, nil)
	if err != nil {
		return err
	}
	// Docktail's deleteService treats 404 as success; so does the probe.
	if status != http.StatusOK && status != http.StatusNotFound {
		return fmt.Errorf("deleting the probe Service (HTTP %d): %s", status, errorMessage(body))
	}
	return nil
}

func servicesScopeError(status int, body []byte) error {
	return fmt.Errorf("the OAuth client is missing the services scope (General -> Services: Write), needed to manage Tailscale Service definitions (HTTP %d): %s", status, errorMessage(body))
}

// tailscaleAPICall performs an authenticated JSON API request and returns the
// status code and (bounded) response body.
func tailscaleAPICall(ctx context.Context, method, endpoint, token string, payload []byte) (int, []byte, error) {
	var reqBody io.Reader
	if payload != nil {
		reqBody = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reqBody)
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

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return 0, nil, fmt.Errorf("reading Tailscale API response: %w", err)
	}
	return resp.StatusCode, body, nil
}

func errorMessage(body []byte) string {
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &e) == nil && e.Message != "" {
		return e.Message
	}
	return strings.TrimSpace(string(body))
}
