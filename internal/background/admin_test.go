package background

import (
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminServerServesHealthOverUnixSocket(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", dir)
	socket := filepath.Join(dir, "once-admin.sock")

	ctx := t.Context()

	srv := NewAdminServer()
	go func() { _ = srv.Run(ctx) }()

	require.Eventually(t, func() bool {
		_, err := net.Dial("unix", socket)
		return err == nil
	}, 2*time.Second, 10*time.Millisecond, "socket never became available")

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socket)
			},
		},
	}

	resp, err := client.Get("http://once-admin/up")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "ok")

	// The config the once-admin container mounts must have been written.
	assert.FileExists(t, filepath.Join(dir, "once-admin-nginx.conf"))
}

func TestAdminServerRemovesSocketOnShutdown(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", dir)
	socket := filepath.Join(dir, "once-admin.sock")

	ctx, cancel := context.WithCancel(context.Background())
	srv := NewAdminServer()
	done := make(chan struct{})
	go func() { _ = srv.Run(ctx); close(done) }()

	require.Eventually(t, func() bool {
		_, err := net.Dial("unix", socket)
		return err == nil
	}, 2*time.Second, 10*time.Millisecond)

	cancel()
	<-done
	assert.NoFileExists(t, socket)
}
