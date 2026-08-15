package integration

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
	"github.com/basecamp/once/internal/handoff"
)

// TestRecoveryHandoffServesThePageThroughTheProxy is the QR path end to end
// without the QR: the temporary kamal-proxy service, the certificate Once mints
// and copies into the Proxy, the daemon serving the page, the Done button
// burning the token, and the teardown removing all of it.
//
// The phone dials an IP literal, and a browser sends no SNI for one, so the
// client here does the same - it is the part of the design the prototype could
// not exercise, because it served the page from its own process.
func TestRecoveryHandoffServesThePageThroughTheProxy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ns, err := docker.NewNamespace("once-handoff-test")
	require.NoError(t, err)
	t.Cleanup(func() { ns.Teardown(context.Background(), true) })

	require.NoError(t, ns.EnsureNetwork(ctx))
	ports := getProxyPorts(t)
	require.NoError(t, ns.Proxy().Boot(ctx, ports))

	// The daemon serves the page; once-admin is what the Proxy targets.
	startAdminServer(t, ctx)
	require.NoError(t, ns.Admin().Boot(ctx))

	kit := handoff.Kit{
		Password: "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567",
		Hostname: "vault-host",
		Details: []handoff.Detail{
			{Label: "Offsite repo", Value: "s3:s3.amazonaws.com/once-backups"},
			{Label: "Credentials", Value: "AWS_ACCESS_KEY_ID (names only)"},
		},
	}

	live, err := ns.StartRecoveryHandoff(ctx, kit)
	require.NoError(t, err)
	t.Cleanup(func() { _ = live.Close(context.Background()) })

	url := live.URL()
	require.True(t, strings.HasPrefix(url, "https://"), "the page is never offered over plain HTTP: %s", url)
	require.NotContains(t, url, kit.Password, "the secret is never in the URL")

	client := handoffClient(ports.HTTPSPort)

	// -- the page serves, with the password in a field a manager will save --
	body, resp := handoffGet(t, client, url)
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", body)
	assert.Contains(t, body, kit.Password)
	assert.Contains(t, body, `autocomplete="new-password"`)
	assert.Contains(t, body, "s3:s3.amazonaws.com/once-backups")

	// The certificate the Proxy served is the one Once minted for this address.
	require.NotNil(t, resp.TLS)
	require.NotEmpty(t, resp.TLS.PeerCertificates)
	address := strings.TrimPrefix(strings.TrimSuffix(url[:strings.LastIndex(url, "/r/")], "/"), "https://")
	assert.Equal(t, address, resp.TLS.PeerCertificates[0].Subject.CommonName)

	// -- saving is a POST-redirect-GET back to the same page, and burns nothing --
	resp = handoffPost(t, client, url+"/saved")
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
	_, resp = handoffGet(t, client, url)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "saving must leave the page in place")

	// -- Done burns the token; the link is spent from then on --
	resp = handoffPost(t, client, url+"/done")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	_, resp = handoffGet(t, client, url)
	assert.Equal(t, http.StatusGone, resp.StatusCode)

	// -- closing removes the route, the certificate and the session --
	require.NoError(t, live.Close(ctx))
	assert.NoFileExists(t, docker.RecoveryHandoffPath())

	certificates, err := ns.Proxy().ExecOutput(ctx, []string{"ls", "/home/kamal-proxy/.config/kamal-proxy"})
	require.NoError(t, err)
	assert.NotContains(t, certificates, "once-recovery", "the certificate and key are removed from the proxy")

	require.Eventually(t, func() bool {
		resp, err := client.Get(url)
		if err != nil {
			// No certificate left to serve is the route being gone too.
			return true
		}
		resp.Body.Close()
		return resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusGone
	}, 30*time.Second, time.Second, "the temporary route is removed from the proxy")
}

// TestRecoveryHandoffNeedsTheDaemon names the missing daemon rather than
// handing the user a QR code that leads nowhere.
func TestRecoveryHandoffNeedsTheDaemon(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	ns, err := docker.NewNamespace("once-handoff-nodaemon-test")
	require.NoError(t, err)
	t.Cleanup(func() { ns.Teardown(context.Background(), true) })

	// A runtime dir with no socket in it is a daemon that is not running.
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", t.TempDir())

	_, err = ns.StartRecoveryHandoff(ctx, handoff.Kit{Password: "ABCDEFGH"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "background daemon")
	assert.NoFileExists(t, docker.RecoveryHandoffPath(), "a failed start leaves no session behind")
}

// Helpers

// handoffClient dials the Proxy's HTTPS port whatever address the URL names,
// so the test can use the real LAN URL against a proxy on a random port. It
// sets no ServerName, which is what a browser does for an IP literal.
func handoffClient(httpsPort int) *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, fmt.Sprintf("127.0.0.1:%d", httpsPort))
			},
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		// The save redirect is the assertion, so it must not be followed.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func handoffGet(t *testing.T, client *http.Client, url string) (string, *http.Response) {
	t.Helper()
	resp, err := client.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body), resp
}

func handoffPost(t *testing.T, client *http.Client, url string) *http.Response {
	t.Helper()
	resp, err := client.Post(url, "application/x-www-form-urlencoded", strings.NewReader(""))
	require.NoError(t, err)
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp
}
