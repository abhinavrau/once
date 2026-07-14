package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Scopes minted from the oauth_keys bootstrap credential: the runtime client
// gets exactly what the enable-time probe proves Docktail and the sidecar
// need; the policy client gets only policy_file for the one-shot ACL write.
var (
	runtimeClientScopes = []string{"auth_keys", "devices:core", "services"}
	policyClientScopes  = []string{"policy_file"}
)

// tsOAuthClientRequest is the keys-endpoint payload that creates an OAuth
// client, byte-compatible with tailscale-client-go-v2's CreateOAuthClient
// (keyType "client"; tags omitted when the scopes don't require them).
type tsOAuthClientRequest struct {
	KeyType     string   `json:"keyType"`
	Scopes      []string `json:"scopes"`
	Tags        []string `json:"tags,omitempty"`
	Description string   `json:"description"`
}

// TailscaleBootstrap mints the runtime and policy OAuth clients from a single
// oauth_keys-scoped bootstrap credential, shrinking the admin-console work to
// creating that one credential. The bootstrap token lives only in memory for
// the enable flow and is never stored.
type TailscaleBootstrap struct {
	baseURL string
	token   string
}

// NewTailscaleBootstrap exchanges the bootstrap OAuth credentials for an
// access token. A failure here means the client ID or secret is wrong or
// revoked.
func NewTailscaleBootstrap(ctx context.Context, clientID, clientSecret string) (*TailscaleBootstrap, error) {
	return newTailscaleBootstrap(ctx, tailscaleAPIBaseURL, clientID, clientSecret)
}

// MintRuntimeClient mints the minimal-scope OAuth client that Docktail and the
// sidecar run with, tagged so it can only mint keys for the given tag. This is
// the only credential that gets persisted (in the once label, by enable).
func (b *TailscaleBootstrap) MintRuntimeClient(ctx context.Context, tag string) (clientID, clientSecret string, err error) {
	return b.mintClient(ctx, tsOAuthClientRequest{
		KeyType:     "client",
		Scopes:      runtimeClientScopes,
		Tags:        policyTags(tag),
		Description: "once-docktail",
	})
}

// MintPolicyClient mints the one-shot policy_file-scoped client used for the
// tailnet policy write. Callers must DeleteClient it after use; it is never
// stored.
func (b *TailscaleBootstrap) MintPolicyClient(ctx context.Context) (clientID, clientSecret string, err error) {
	return b.mintClient(ctx, tsOAuthClientRequest{
		KeyType:     "client",
		Scopes:      policyClientScopes,
		Description: "once-policy",
	})
}

// DeleteClient revokes a minted OAuth client (oauth_keys covers deletion), so
// the one-shot policy client doesn't outlive its single use.
func (b *TailscaleBootstrap) DeleteClient(ctx context.Context, clientID string) error {
	status, body, err := tailscaleAPICall(ctx, http.MethodDelete, b.baseURL+"/tailnet/-/keys/"+url.PathEscape(clientID), b.token, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("deleting the %s OAuth client (HTTP %d): %s", clientID, status, errorMessage(body))
	}
	return nil
}

// Private

// mintClient creates an OAuth client via the keys endpoint. The response's id
// is the new client's ID and key is its secret, per the trust-credentials API.
func (b *TailscaleBootstrap) mintClient(ctx context.Context, reqBody tsOAuthClientRequest) (string, string, error) {
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", err
	}

	status, body, err := tailscaleAPICall(ctx, http.MethodPost, b.baseURL+"/tailnet/-/keys", b.token, payload)
	if err != nil {
		return "", "", err
	}
	switch {
	case status == http.StatusForbidden:
		return "", "", fmt.Errorf("the bootstrap OAuth client is missing the oauth_keys scope (Keys -> OAuth Keys: Write), needed to mint the %s OAuth client (HTTP %d): %s", reqBody.Description, status, errorMessage(body))
	case status != http.StatusOK && len(reqBody.Tags) > 0:
		return "", "", fmt.Errorf("Tailscale refused to mint the %s OAuth client for tags %s (HTTP %d): %s; ensure the tags are defined in your tailnet policy (Access Controls -> tagOwners)", reqBody.Description, strings.Join(reqBody.Tags, ","), status, errorMessage(body))
	case status != http.StatusOK:
		return "", "", fmt.Errorf("Tailscale refused to mint the %s OAuth client (HTTP %d): %s", reqBody.Description, status, errorMessage(body))
	}

	var respBody struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal(body, &respBody); err != nil {
		return "", "", fmt.Errorf("decoding Tailscale OAuth client response: %w", err)
	}
	return respBody.ID, respBody.Key, nil
}

// Helpers

func newTailscaleBootstrap(ctx context.Context, baseURL, clientID, clientSecret string) (*TailscaleBootstrap, error) {
	token, err := tailscaleOAuthToken(ctx, baseURL, clientID, clientSecret)
	if err != nil {
		return nil, err
	}
	return &TailscaleBootstrap{baseURL: baseURL, token: token}, nil
}
