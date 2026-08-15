package handoff

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testKit() Kit {
	return Kit{
		Password: "ABCD-EFGH-IJKL-MNOP-QRST-UVWX-YZ23-4567",
		Hostname: "backup-host",
		Details: []Detail{
			{Label: "Offsite repo", Value: "s3:s3.amazonaws.com/once-backups"},
			{Label: "Credentials", Value: "AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY (names only)"},
		},
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(filepath.Join(t.TempDir(), "handoff.json"))
}

func TestStoreCreateMintsADistinctToken(t *testing.T) {
	store := newTestStore(t)

	first, err := store.Create(testKit(), time.Minute)
	require.NoError(t, err)
	second, err := store.Create(testKit(), time.Minute)
	require.NoError(t, err)

	assert.NotEqual(t, first, second)
	assert.Len(t, first, tokenLength)
	// The token rides in a URL and a QR payload: keep it URL-safe.
	assert.Equal(t, first, url.PathEscape(first))
}

func TestStoreWritesOwnerOnlyAndClearRemoves(t *testing.T) {
	store := newTestStore(t)
	_, err := store.Create(testKit(), time.Minute)
	require.NoError(t, err)

	info, err := os.Stat(store.path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	require.NoError(t, store.Clear())
	_, err = os.Stat(store.path)
	assert.True(t, os.IsNotExist(err))
	assert.NoError(t, store.Clear(), "clearing an absent session is not an error")
}

func TestStoreLookupRejectsWrongExpiredAndBurnedTokens(t *testing.T) {
	store := newTestStore(t)
	token, err := store.Create(testKit(), time.Minute)
	require.NoError(t, err)

	_, ok := store.lookup(token)
	assert.True(t, ok)

	_, ok = store.lookup(strings.Repeat("A", tokenLength))
	assert.False(t, ok, "a wrong token of the same length is refused")

	_, ok = store.lookup("")
	assert.False(t, ok)

	require.NoError(t, store.burn(token))
	_, ok = store.lookup(token)
	assert.False(t, ok, "a burned token stays burned")

	expired, err := store.Create(testKit(), -time.Second)
	require.NoError(t, err)
	_, ok = store.lookup(expired)
	assert.False(t, ok, "an expired token is refused")
}

func TestStoreLookupWithNoSession(t *testing.T) {
	store := newTestStore(t)
	_, ok := store.lookup(strings.Repeat("A", tokenLength))
	assert.False(t, ok)
}

// get and post drive the handler the way the phone does.
func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func post(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
	return rec
}

func TestHandlerServesThePasswordInAFormField(t *testing.T) {
	store := newTestStore(t)
	kit := testKit()
	token, err := store.Create(kit, time.Minute)
	require.NoError(t, err)

	rec := get(t, Handler(store), PagePath(token))
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, kit.Password)
	// The save prompt only fires for a password field the manager recognizes.
	assert.Contains(t, body, `autocomplete="new-password"`)
	assert.Contains(t, body, `autocomplete="username"`)
	assert.Contains(t, body, kit.Hostname)
	for _, detail := range kit.Details {
		assert.Contains(t, body, detail.Value)
	}
	// A secret must never be cached or handed to a referrer.
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.Equal(t, "no-referrer", rec.Header().Get("Referrer-Policy"))
}

func TestHandlerSavedIsPostRedirectGetBackToTheSamePage(t *testing.T) {
	store := newTestStore(t)
	token, err := store.Create(testKit(), time.Minute)
	require.NoError(t, err)
	h := Handler(store)

	rec := post(t, h, PagePath(token)+"/saved")
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, PagePath(token), rec.Header().Get("Location"))

	// Saving does not burn: the page must still be there to come back to.
	assert.Equal(t, http.StatusOK, get(t, h, PagePath(token)).Code)
}

func TestHandlerDoneBurnsTheTokenAndLaterVisitsAreGone(t *testing.T) {
	store := newTestStore(t)
	token, err := store.Create(testKit(), time.Minute)
	require.NoError(t, err)
	h := Handler(store)

	rec := post(t, h, PagePath(token)+"/done")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "no longer works")

	assert.Equal(t, http.StatusGone, get(t, h, PagePath(token)).Code)
	assert.Equal(t, http.StatusGone, post(t, h, PagePath(token)+"/saved").Code)
	assert.Equal(t, http.StatusGone, post(t, h, PagePath(token)+"/done").Code)
}

func TestHandlerNeverLeaksTheSecretOnARefusedRequest(t *testing.T) {
	store := newTestStore(t)
	kit := testKit()
	token, err := store.Create(kit, time.Minute)
	require.NoError(t, err)
	h := Handler(store)

	for _, path := range []string{
		PagePath(strings.Repeat("A", tokenLength)),
		PagePath(token) + "/nope",
		pathPrefix,
	} {
		rec := get(t, h, path)
		assert.NotContains(t, rec.Body.String(), kit.Password, path)
		assert.NotEqual(t, http.StatusOK, rec.Code, path)
	}
}

func TestHandlerExpiredTokenIsGone(t *testing.T) {
	store := newTestStore(t)
	token, err := store.Create(testKit(), -time.Second)
	require.NoError(t, err)

	assert.Equal(t, http.StatusGone, get(t, Handler(store), PagePath(token)).Code)
}

func TestPagePathIsShortEnoughForTheQRRowBudget(t *testing.T) {
	// The QR view has 24 rows to live in, and the payload length decides the
	// symbol version. Keep the path short so a LAN URL stays small.
	assert.LessOrEqual(t, len(PagePath(strings.Repeat("A", tokenLength))), 20)
}
