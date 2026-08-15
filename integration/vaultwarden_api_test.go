package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// vaultwardenClient speaks the Bitwarden API that Vaultwarden serves, so the
// acceptance test writes and reads vault data the way a real client does
// rather than by reaching into the volume. Everything a client sends is
// already encrypted, so the values here are opaque strings in the same shape -
// what matters is that the bytes come back identical after a restore.
type vaultwardenClient struct {
	t       *testing.T
	baseURL string
	http    *http.Client
	token   string
}

// vaultItem is one item as the vault holds it: the encrypted fields plus the
// attachment's bytes. Comparing two of these is the integrity assertion.
type vaultItem struct {
	Name           string
	Username       string
	Password       string
	AttachmentName string
	Attachment     []byte
}

func newVaultwardenClient(t *testing.T, baseURL string) *vaultwardenClient {
	t.Helper()

	return &vaultwardenClient{
		t:       t,
		baseURL: strings.TrimSuffix(baseURL, "/"),
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *vaultwardenClient) Register(email, password string) {
	c.t.Helper()

	key := encryptedString("symmetric-key-for-" + email)
	c.postJSON("/identity/accounts/register", map[string]any{
		"email":              email,
		"name":               "Once Acceptance",
		"masterPasswordHash": masterPasswordHash(email, password),
		"masterPasswordHint": "",
		"key":                key,
		"kdf":                0,
		"kdfIterations":      600000,
		"keys": map[string]string{
			"publicKey":           base64.StdEncoding.EncodeToString([]byte("public-key-" + email)),
			"encryptedPrivateKey": key,
		},
	}, nil)
}

// LogIn exchanges the master password hash for an access token, which is also
// the proof that the restored vault still knows this account.
func (c *vaultwardenClient) LogIn(email, password string) {
	c.t.Helper()

	form := url.Values{
		"grant_type":       {"password"},
		"username":         {email},
		"password":         {masterPasswordHash(email, password)},
		"scope":            {"api offline_access"},
		"client_id":        {"web"},
		"deviceType":       {"9"},
		"deviceIdentifier": {"once-acceptance-test"},
		"deviceName":       {"once-acceptance-test"},
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/identity/connect/token", strings.NewReader(form.Encode()))
	require.NoError(c.t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Auth-Email", base64.RawURLEncoding.EncodeToString([]byte(email)))

	var body struct {
		AccessToken string `json:"access_token"`
	}
	c.do(req, &body)
	require.NotEmpty(c.t, body.AccessToken, "vaultwarden returned no access token")
	c.token = body.AccessToken
}

// AddItem stores one item with its attachment, and returns it as the vault now
// holds it.
func (c *vaultwardenClient) AddItem(name, username, password, attachmentName string, attachment []byte) vaultItem {
	c.t.Helper()

	item := vaultItem{
		Name:           encryptedString(name),
		Username:       encryptedString(username),
		Password:       encryptedString(password),
		AttachmentName: encryptedString(attachmentName),
		Attachment:     attachment,
	}

	var created struct {
		ID string `json:"id"`
	}
	c.postJSON("/api/ciphers", map[string]any{
		"type":     1,
		"name":     item.Name,
		"favorite": false,
		"reprompt": 0,
		"login": map[string]any{
			"username": item.Username,
			"password": item.Password,
		},
	}, &created)
	require.NotEmpty(c.t, created.ID)

	c.attach(created.ID, item.AttachmentName, attachment)
	return item
}

// Items reads the whole vault back, attachment bytes included.
func (c *vaultwardenClient) Items() []vaultItem {
	c.t.Helper()

	var sync struct {
		Ciphers []struct {
			Name  string `json:"name"`
			Login struct {
				Username string `json:"username"`
				Password string `json:"password"`
			} `json:"login"`
			Attachments []struct {
				FileName string `json:"fileName"`
				URL      string `json:"url"`
			} `json:"attachments"`
		} `json:"ciphers"`
	}
	c.getJSON("/api/sync?excludeDomains=true", &sync)

	items := make([]vaultItem, 0, len(sync.Ciphers))
	for _, cipher := range sync.Ciphers {
		item := vaultItem{
			Name:     cipher.Name,
			Username: cipher.Login.Username,
			Password: cipher.Login.Password,
		}
		if len(cipher.Attachments) > 0 {
			item.AttachmentName = cipher.Attachments[0].FileName
			item.Attachment = c.download(cipher.Attachments[0].URL)
		}
		items = append(items, item)
	}
	return items
}

// Private

func (c *vaultwardenClient) attach(cipherID, fileName string, data []byte) {
	c.t.Helper()

	var slot struct {
		AttachmentID string `json:"attachmentId"`
	}
	c.postJSON(fmt.Sprintf("/api/ciphers/%s/attachment/v2", cipherID), map[string]any{
		"key":          encryptedString("attachment-key"),
		"fileName":     fileName,
		"fileSize":     len(data),
		"adminRequest": false,
	}, &slot)
	require.NotEmpty(c.t, slot.AttachmentID)

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("data", "attachment")
	require.NoError(c.t, err)
	_, err = part.Write(data)
	require.NoError(c.t, err)
	require.NoError(c.t, form.Close())

	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/api/ciphers/%s/attachment/%s", c.baseURL, cipherID, slot.AttachmentID), &body)
	require.NoError(c.t, err)
	req.Header.Set("Content-Type", form.FormDataContentType())
	c.do(req, nil)
}

// download fetches an attachment. Vaultwarden builds the URL from DOMAIN, so
// only its path and its single-use token are taken - the host is this test's
// own route to the app, which differs between the two installs.
func (c *vaultwardenClient) download(attachmentURL string) []byte {
	c.t.Helper()

	parsed, err := url.Parse(attachmentURL)
	require.NoError(c.t, err)

	req, err := http.NewRequest(http.MethodGet, c.baseURL+parsed.RequestURI(), nil)
	require.NoError(c.t, err)

	resp, err := c.http.Do(req)
	require.NoError(c.t, err)
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	require.NoError(c.t, err)
	require.Equal(c.t, http.StatusOK, resp.StatusCode, "GET %s: %s", parsed.Path, data)
	return data
}

func (c *vaultwardenClient) postJSON(path string, payload any, out any) {
	c.t.Helper()

	body, err := json.Marshal(payload)
	require.NoError(c.t, err)

	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	require.NoError(c.t, err)
	req.Header.Set("Content-Type", "application/json")
	c.do(req, out)
}

func (c *vaultwardenClient) getJSON(path string, out any) {
	c.t.Helper()

	req, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	require.NoError(c.t, err)
	c.do(req, out)
}

func (c *vaultwardenClient) do(req *http.Request, out any) {
	c.t.Helper()

	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	require.NoError(c.t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(c.t, err)
	require.Equal(c.t, http.StatusOK, resp.StatusCode, "%s %s: %s", req.Method, req.URL.Path, body)

	if out != nil {
		require.NoError(c.t, json.Unmarshal(body, out), "%s %s: %s", req.Method, req.URL.Path, body)
	}
}

// Helpers

// encryptedString shapes a value the way a Bitwarden client hands one over: an
// AES-CBC-HMAC EncString, whose parts have to decode and whose MAC has to be
// the right length for Vaultwarden to accept it.
func encryptedString(value string) string {
	mac := sha256.Sum256([]byte(value))
	return fmt.Sprintf("2.%s|%s|%s",
		base64.StdEncoding.EncodeToString(mac[:16]),
		base64.StdEncoding.EncodeToString([]byte(value)),
		base64.StdEncoding.EncodeToString(mac[:]))
}

func masterPasswordHash(email, password string) string {
	sum := sha256.Sum256([]byte(password + ":" + email))
	return base64.StdEncoding.EncodeToString(sum[:])
}
