package logging

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrapTransportLogsMetadataAtDebug(t *testing.T) {
	logged := captureLog(t, true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		w.Write([]byte("secret response body"))
	}))
	defer server.Close()

	client := &http.Client{Transport: WrapTransport(nil)}
	resp, err := client.Post(server.URL+"/path?q=1", "text/plain", strings.NewReader("secret request body"))
	require.NoError(t, err)
	resp.Body.Close()

	line := logged.String()
	assert.Contains(t, line, "method=POST")
	assert.Contains(t, line, server.URL+"/path?q=1")
	assert.Contains(t, line, "status=418")
	assert.Contains(t, line, "duration=")
	assert.NotContains(t, line, "secret")
}

func TestWrapTransportLogsErrors(t *testing.T) {
	logged := captureLog(t, true)

	client := &http.Client{Transport: WrapTransport(nil)}
	_, err := client.Get("http://127.0.0.1:1/unreachable")
	require.Error(t, err)

	line := logged.String()
	assert.Contains(t, line, "method=GET")
	assert.Contains(t, line, "error=")
	assert.NotContains(t, line, "status=")
}

func TestWrapTransportRedactsURLUserinfo(t *testing.T) {
	logged := captureLog(t, true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	url := strings.Replace(server.URL, "http://", "http://user:hunter2@", 1)
	client := &http.Client{Transport: WrapTransport(nil)}
	resp, err := client.Get(url)
	require.NoError(t, err)
	resp.Body.Close()

	assert.NotContains(t, logged.String(), "hunter2")
}

func TestWrapTransportSilentWithoutVerbose(t *testing.T) {
	logged := captureLog(t, false)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	client := &http.Client{Transport: WrapTransport(nil)}
	resp, err := client.Get(server.URL)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Empty(t, logged.String())
}

// Helpers

func captureLog(t *testing.T, verbose bool) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	prev := slog.Default()
	prevLevel := level.Level()
	t.Cleanup(func() {
		slog.SetDefault(prev)
		level.Set(prevLevel)
	})

	SetVerbose(verbose)
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: &level})))

	return &buf
}
