package integration

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/background"
	"github.com/basecamp/once/internal/docker"
	"github.com/basecamp/once/internal/ui"
)

func TestUIInstallAndManageApp(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ns, err := docker.NewNamespace("once-ui-test")
	require.NoError(t, err)
	defer ns.Teardown(ctx, true)

	require.NoError(t, ns.EnsureNetwork(ctx))
	proxyPorts := getProxyPorts(t)
	require.NoError(t, ns.Proxy().Boot(ctx, proxyPorts))

	app := ui.NewApp(ns, "")
	d := newAppDriver(t, app)
	d.start()

	d.send(tea.WindowSizeMsg{Width: 120, Height: 40})

	// -- Screen 1: App list → select "Custom Docker image" --
	d.send(keyMsg("up"))
	d.send(keyMsg("enter"))

	d.waitForView("Image", 5*time.Second)

	// -- Screen 2: Image form --
	d.typeText("ghcr.io/basecamp/once-campfire:main")
	d.send(keyMsg("tab"))
	d.send(keyMsg("enter"))

	d.waitForView("Hostname", 5*time.Second)

	// -- Screen 3: Hostname form --
	d.typeText("chat.localhost")
	d.send(keyMsg("tab"))
	d.send(keyMsg("enter"))

	// -- Install activity → dashboard --
	// Wait for "running" which only appears in the dashboard panel state info,
	// not in the install form or activity views.
	d.waitForView("running", 5*time.Minute)
	assert.Contains(t, d.viewContent(), "chat.localhost")

	// Verify the app is reachable via HTTP
	appURL := fmt.Sprintf("http://chat.localhost:%d", proxyPorts.HTTPPort)
	resp, err := http.Get(appURL)
	require.NoError(t, err)
	resp.Body.Close()
	assert.True(t, resp.StatusCode >= 200 && resp.StatusCode < 400,
		"expected 2xx/3xx, got %d", resp.StatusCode)

	// -- Stop via actions menu --
	d.send(keyMsg("a"))
	d.send(keyMsg("s"))
	d.waitForView("stopped", 30*time.Second)

	// -- Start via actions menu --
	d.send(keyMsg("a"))
	d.send(keyMsg("s"))
	d.waitForView("running", 30*time.Second)

	// -- Remove via actions menu --
	d.send(keyMsg("a"))
	d.send(keyMsg("r"))
	d.waitForView("Remove application and data?", 10*time.Second)
	d.send(keyMsg("tab"))
	d.send(keyMsg("enter"))
	d.waitForView("There are no applications installed", 30*time.Second)
}

// TestUITailscaleFormEnablesViaControlSeam drives the dashboard's global
// Tailscale settings form (the `t` overlay) end to end: open it, fill the
// credentials, submit, and assert it ran the same enable lifecycle the CLI uses
// — once-tsdproxy boots, the deployed app retrofits its tsdproxy.* labels and
// registers a node, and once-admin comes up. It also checks esc dismissal and
// that re-opening the form pre-populates from the stored settings.
func TestUITailscaleFormEnablesViaControlSeam(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ns, err := docker.NewNamespace("once-ts-ui-test")
	require.NoError(t, err)
	require.NoError(t, ns.EnsureNetwork(ctx))
	require.NoError(t, ns.Proxy().Boot(ctx, getProxyPorts(t)))
	t.Cleanup(func() { ns.Teardown(context.Background(), true) })

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	t.Cleanup(func() { cli.Close() })

	cp := newControlPlane(t, ctx, ns)
	key, err := cp.authKey(ctx)
	require.NoError(t, err)
	t.Setenv("ONCE_TAILSCALE_CONTROL_URL", cp.controlURL())
	t.Setenv("ONCE_TAILSCALE_AUTH_KEY", key)

	// Enable boots once-admin, which bind-mounts the daemon's Unix socket; run the
	// admin server in-process and wait for the socket so Boot doesn't fail.
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", t.TempDir())
	adminCtx, stopAdmin := context.WithCancel(ctx)
	t.Cleanup(stopAdmin)
	go func() { _ = background.NewAdminServer().Run(adminCtx) }()
	require.Eventually(t, func() bool {
		_, err := net.Dial("unix", docker.AdminSocketPath())
		return err == nil
	}, 5*time.Second, 50*time.Millisecond, "admin socket never became available")

	deployApp(t, ctx, ns, docker.ApplicationSettings{
		Name:  "whoami",
		Image: whoamiImage,
		Host:  "whoami.localhost",
	})

	app := ui.NewApp(ns, "")
	d := newAppDriver(t, app)
	d.start()
	d.send(tea.WindowSizeMsg{Width: 120, Height: 40})

	// -- esc dismisses the overlay --
	d.send(keyMsg("t"))
	d.waitForView("OAuth Client ID", 5*time.Second)
	d.send(keyMsg("esc"))
	d.waitUntil(func() bool {
		return !strings.Contains(d.viewContent(), "OAuth Client ID")
	}, 5*time.Second)

	// -- open the form and enable Tailscale --
	d.send(keyMsg("t"))
	d.waitForView("OAuth Client ID", 5*time.Second)
	d.send(keyMsg("space")) // toggle Enable Tailscale on
	d.send(keyMsg("tab"))   // → OAuth Client ID
	d.typeText("unused-with-control-seam")
	d.send(keyMsg("tab")) // → OAuth Client Secret
	d.typeText("unused-with-control-seam")
	d.send(keyMsg("tab")) // → Tag (required by the form; unused under the control seam)
	d.typeText("tag:once")
	d.send(keyMsg("tab"))   // → Save
	d.send(keyMsg("enter")) // submit

	// The form runs the real EnableTailscale and stays open (showing progress,
	// which replaces the field labels) until the lifecycle finishes, then closes.
	// Wait on the overlay's "esc close" help, which is present in both the form
	// and progress states but gone once closed. Pump the UI while it runs; on a
	// failure the form stays open with an error and this times out.
	d.waitUntil(func() bool {
		return !strings.Contains(d.viewContent(), "close")
	}, 3*time.Minute)

	// once-tsdproxy booted and the retrofitted app registered a node.
	_, err = cli.ContainerInspect(ctx, ns.Name()+"-tsdproxy")
	require.NoError(t, err, "once-tsdproxy should be running after enabling via the form")

	require.Eventually(t, func() bool {
		names, err := cp.nodeNames(ctx)
		return err == nil && slices.Contains(names, "whoami")
	}, 90*time.Second, 2*time.Second, "app never registered a node after enabling via the form")

	assertAppLabels(t, ctx, cli, ns, hasTSDProxyLabels)

	_, err = cli.ContainerInspect(ctx, ns.Name()+"-admin")
	require.NoError(t, err, "once-admin should be running after enabling via the form")

	// -- re-opening the form pre-populates from the stored settings --
	d.send(keyMsg("t"))
	d.waitForView("OAuth Client ID", 5*time.Second)
	view := d.viewContent()
	assert.Contains(t, view, "unused-with-control-seam", "form should pre-populate the stored client ID")
	assert.Contains(t, view, "[✓]", "Enable checkbox should be checked when Tailscale is already enabled")
}

// appDriver drives a ui.App outside of tea.Program by manually executing
// commands and feeding their results back through a message channel.
type appDriver struct {
	t     *testing.T
	app   *ui.App
	msgCh chan tea.Msg
}

func newAppDriver(t *testing.T, app *ui.App) *appDriver {
	return &appDriver{
		t:     t,
		app:   app,
		msgCh: make(chan tea.Msg, 256),
	}
}

// start enqueues the commands returned by App.Init, including the Docker
// event watcher and scrape timers.
func (d *appDriver) start() {
	d.enqueueCmd(d.app.Init())
}

// send flushes any pending messages, then delivers msg to App.Update and
// enqueues the returned command. All App.Update calls happen on the caller's
// goroutine, so there is no concurrent access.
func (d *appDriver) send(msg tea.Msg) {
	d.flush()
	_, cmd := d.app.Update(msg)
	d.enqueueCmd(cmd)
}

func (d *appDriver) typeText(text string) {
	for _, r := range text {
		d.send(keyMsg(string(r)))
	}
}

func (d *appDriver) viewContent() string {
	return d.app.View().Content
}

// waitForView processes messages from the channel until the app's view
// contains target, or until timeout.
func (d *appDriver) waitForView(target string, timeout time.Duration) {
	d.t.Helper()
	deadline := time.After(timeout)
	for {
		if strings.Contains(d.viewContent(), target) {
			return
		}
		select {
		case msg := <-d.msgCh:
			d.processMsg(msg)
		case <-deadline:
			d.t.Fatalf("timed out waiting for view to contain %q\n\nView:\n%s",
				target, d.viewContent())
		}
	}
}

// waitUntil pumps messages on the test goroutine until cond holds, or fails at
// timeout. Unlike waitForView it can wait for a view to *stop* containing
// something (e.g. an overlay closing).
func (d *appDriver) waitUntil(cond func() bool, timeout time.Duration) {
	d.t.Helper()
	deadline := time.After(timeout)
	for {
		d.flush()
		if cond() {
			return
		}
		select {
		case msg := <-d.msgCh:
			d.processMsg(msg)
		case <-deadline:
			d.t.Fatalf("timed out waiting for condition\n\nView:\n%s", d.viewContent())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// flush drains any immediately available messages from the channel.
func (d *appDriver) flush() {
	for {
		select {
		case msg := <-d.msgCh:
			d.processMsg(msg)
		default:
			return
		}
	}
}

func (d *appDriver) processMsg(msg tea.Msg) {
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, cmd := range batch {
			d.enqueueCmd(cmd)
		}
		return
	}
	_, cmd := d.app.Update(msg)
	d.enqueueCmd(cmd)
}

func (d *appDriver) enqueueCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if msg != nil {
			d.msgCh <- msg
		}
	}()
}

func keyMsg(s string) tea.KeyPressMsg {
	k := tea.Key{Text: s}
	if r := []rune(s); len(r) == 1 {
		k.Code = r[0]
	}
	return tea.KeyPressMsg(k)
}
