package background

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
	"github.com/basecamp/once/internal/handoff"
)

// startAdminServerForTest runs the daemon's admin server against a temporary
// runtime dir and returns a client that speaks HTTP over its Unix socket, the
// way the once-admin container's nginx does.
func startAdminServerForTest(t *testing.T) *http.Client {
	t.Helper()
	socket := docker.AdminSocketPath()

	srv := NewAdminServer()
	go func() { _ = srv.Run(t.Context()) }()

	require.Eventually(t, func() bool {
		_, err := net.Dial("unix", socket)
		return err == nil
	}, 2*time.Second, 10*time.Millisecond, "socket never became available")

	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socket)
			},
		},
	}
}

func TestAdminServerServesHealthOverUnixSocket(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", dir)

	client := startAdminServerForTest(t)

	resp, err := client.Get("http://once-admin/up")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "ok")
}

// TestAdminServerClaimsADirectoryAtItsSocketPath covers the host a reboot left
// with an empty directory where the socket goes, the debris dockerd's bind
// auto-creation leaves: the daemon clears it rather than crash-looping.
func TestAdminServerClaimsADirectoryAtItsSocketPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", dir)
	require.NoError(t, os.MkdirAll(docker.AdminSocketPath(), 0o755))

	client := startAdminServerForTest(t)

	resp, err := client.Get("http://once-admin/up")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestAdminServerServesTheRecoveryHandoff is the seam between the two
// processes: the custody flow writes a session, and the daemon - which never
// hears about it directly - serves that session's page and burns it.
func TestAdminServerServesTheRecoveryHandoff(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", dir)

	client := startAdminServerForTest(t)
	password := "ABCD-EFGH-IJKL-MNOP-QRST-UVWX-YZ23-4567"

	// No handoff is live: the routes are dark rather than absent.
	resp, err := client.Get("http://once-admin" + handoff.PagePath("AAAAAAAAAAAAAAAA"))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusGone, resp.StatusCode)

	// The custody flow, in the other process, publishes the kit.
	store := handoff.NewStore(docker.RecoveryHandoffPath())
	token, err := store.Create(handoff.Kit{Password: password, Hostname: "vault-host"}, handoff.TTL)
	require.NoError(t, err)

	resp, err = client.Get("http://once-admin" + handoff.PagePath(token))
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(body), password)

	// Done burns it, and the link is spent from then on.
	resp, err = client.Post("http://once-admin"+handoff.PagePath(token)+"/done", "", nil)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, err = client.Get("http://once-admin" + handoff.PagePath(token))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusGone, resp.StatusCode)
}

func TestAdminServerRemovesSocketOnShutdown(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", dir)
	socket := docker.AdminSocketPath()

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
