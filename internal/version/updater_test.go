package version

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsRelease(t *testing.T) {
	for _, v := range []string{"v0.1.10", "v1.2.3", "v10.0.0"} {
		assert.True(t, IsRelease(v), v)
	}
	for _, v := range []string{"dev", "", "v0.1.10-341-g52589e2", "v0.1.10-dirty", "v0.1.10-341-g52589e2-dirty", "52589e2", "0.1.10", "v1.2", "v1.2.3.4", "v01.2.3", "v1.2.3-rc1"} {
		assert.False(t, IsRelease(v), v)
	}
}

func TestUpdateBinary_AlreadyLatest(t *testing.T) {
	srv := releaseServer(t, "v1.0.0", nil)
	u := newUpdater("v1.0.0", srv.URL, srv.Client())

	updated, err := u.UpdateBinary()
	require.NoError(t, err)
	assert.False(t, updated)
}

func TestUpdateBinary_NeverDowngrades(t *testing.T) {
	srv := releaseServer(t, "v0.1.9", nil)
	u := newUpdater("v0.1.10", srv.URL, srv.Client())

	updated, err := u.UpdateBinary()
	require.NoError(t, err)
	assert.False(t, updated)
}

func TestUpdateBinary_UpdatesToNumericallyNewerRelease(t *testing.T) {
	srv := releaseServer(t, "v0.1.10", nil)
	u := newUpdater("v0.1.9", srv.URL, srv.Client())

	// Reaching the asset lookup means v0.1.10 was judged newer than v0.1.9.
	_, err := u.UpdateBinary()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no release asset found")
}

func TestUpdateBinary_RefusesNonReleaseBuild(t *testing.T) {
	for _, v := range []string{"dev", "v0.1.10-341-g52589e2", "v0.1.10-dirty"} {
		var called bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		}))
		t.Cleanup(srv.Close)

		u := newUpdater(v, srv.URL, srv.Client())
		updated, err := u.UpdateBinary()
		require.ErrorIs(t, err, ErrNonReleaseBuild, v)
		assert.False(t, updated)
		assert.False(t, called, v)
	}
}

func TestUpdateBinary_RejectsNonReleaseLatestTag(t *testing.T) {
	srv := releaseServer(t, "nightly", nil)
	u := newUpdater("v1.0.0", srv.URL, srv.Client())

	updated, err := u.UpdateBinary()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nightly")
	assert.False(t, updated)
}

func TestUpdateBinary_NewerVersion(t *testing.T) {
	dir := t.TempDir()

	// Write a fake "current binary"
	execPath := filepath.Join(dir, "once")
	require.NoError(t, os.WriteFile(execPath, []byte("old"), 0755))

	// Serve a fake binary as the download asset
	fakeBinary := []byte("new binary content")
	assetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(fakeBinary)
	}))
	t.Cleanup(assetSrv.Close)

	assets := []asset{{
		Name: "once-linux-amd64",
		URL:  assetSrv.URL + "/once-linux-amd64",
	}}
	srv := releaseServer(t, "v2.0.0", assets)
	u := newUpdater("v1.0.0", srv.URL, srv.Client())

	err := u.replaceBinary(execPath, writeTemp(t, dir, fakeBinary))
	require.NoError(t, err)

	got, err := os.ReadFile(execPath)
	require.NoError(t, err)
	assert.Equal(t, fakeBinary, got)

	// .old file should be cleaned up
	_, err = os.Stat(execPath + ".old")
	assert.True(t, os.IsNotExist(err))
}

func TestUpdateBinary_AssetNotFoundError(t *testing.T) {
	srv := releaseServer(t, "v2.0.0", nil) // no assets at all
	u := newUpdater("v1.0.0", srv.URL, srv.Client())

	_, err := u.UpdateBinary()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no release asset found")
}

func TestUpdateBinary_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	u := newUpdater("v1.0.0", srv.URL, srv.Client())
	_, err := u.UpdateBinary()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status 500")
}

func TestUpdateBinary_DownloadFails(t *testing.T) {
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(badSrv.Close)

	assets := []asset{{
		Name: "once-linux-amd64",
		URL:  badSrv.URL + "/once-linux-amd64",
	}}
	srv := releaseServer(t, "v2.0.0", assets)
	u := newUpdater("v1.0.0", srv.URL, srv.Client())
	u.client = badSrv.Client()

	// Test download failure by calling downloadBinary directly
	dir := t.TempDir()
	err := u.downloadBinary(badSrv.URL+"/once-linux-amd64", filepath.Join(dir, "tmp"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status 404")
}

func TestReplaceBinary(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "once")
	require.NoError(t, os.WriteFile(execPath, []byte("old"), 0755))

	newContent := []byte("new")
	tmpPath := writeTemp(t, dir, newContent)

	u := newUpdater("v1.0.0", "", nil)
	err := u.replaceBinary(execPath, tmpPath)
	require.NoError(t, err)

	got, err := os.ReadFile(execPath)
	require.NoError(t, err)
	assert.Equal(t, newContent, got)

	_, err = os.Stat(execPath + ".old")
	assert.True(t, os.IsNotExist(err))
}

func TestGitHubToken_SentAsHeader(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(release{TagName: "v1.0.0"})
	}))
	t.Cleanup(srv.Close)

	u := newUpdater("v1.0.0", srv.URL, srv.Client())
	u.githubToken = "test-token"

	_, err := u.UpdateBinary()
	require.NoError(t, err)
	assert.Equal(t, "token test-token", gotHeader)
}

// Helpers

func releaseServer(t *testing.T, tag string, assets []asset) *httptest.Server {
	t.Helper()
	rel := release{TagName: tag, Assets: assets}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rel)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeTemp(t *testing.T, dir string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, "once-tmp")
	require.NoError(t, os.WriteFile(path, content, 0755))
	return path
}
