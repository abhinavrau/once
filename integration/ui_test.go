package integration

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
	"github.com/basecamp/once/internal/ui"
)

func TestUIInstallAndManageApp(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ns, err := docker.NewNamespace("once-ui-test")
	require.NoError(t, err)
	// Fresh context: the test's own ctx may already be expired by cleanup time,
	// which would silently fail every removal and strand the containers.
	t.Cleanup(func() { ns.Teardown(context.Background(), true) })

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

	d.waitForView("Exposure", 5*time.Second)

	// -- Screen 3: Exposure form: local preselected (Tailscale is off) with
	// the name-derived host suggestion prefilled; accept both. --
	view := d.viewContent()
	assert.Contains(t, view, "(•) Local", "local should be preselected without Tailscale")
	assert.Contains(t, view, "once-campfire.localhost", "host should prefill the derived suggestion")

	// -- Selecting tailnet exposure while Tailscale is disabled offers the
	// enable overlay inline; dismissing it returns to the filled-in form. --
	d.send(keyMsg("right")) // Local → Tailnet
	d.send(keyMsg("tab"))   // → Host
	d.send(keyMsg("tab"))   // → Install
	d.send(keyMsg("enter"))
	d.waitForView("Client ID", 5*time.Second)
	d.send(keyMsg("esc"))
	d.waitUntil(func() bool {
		return !strings.Contains(d.viewContent(), "Client ID")
	}, 5*time.Second)

	// Back on the form with focus still on Install: wrap around to the
	// exposure radio, return to local, and install.
	d.send(keyMsg("tab"))  // → Cancel
	d.send(keyMsg("tab"))  // → Exposure
	d.send(keyMsg("left")) // Tailnet → Local
	d.send(keyMsg("tab"))  // → Host (keep the suggestion)
	d.send(keyMsg("tab"))  // → Install
	d.send(keyMsg("enter"))

	// -- Install activity → dashboard --
	// Wait for "running" which only appears in the dashboard panel state info,
	// not in the install form or activity views.
	d.waitForView("running", 5*time.Minute)
	assert.Contains(t, d.viewContent(), "once-campfire.localhost")

	// Verify the app is reachable via HTTP
	appURL := fmt.Sprintf("http://once-campfire.localhost:%d", proxyPorts.HTTPPort)
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

	// The app name carries a unique suffix, so capture it before removing to
	// find the volume afterwards.
	require.Len(t, ns.Applications(), 1)
	appName := ns.Applications()[0].Settings.Name

	// -- Remove via actions menu: keep-data is the default choice --
	d.send(keyMsg("a"))
	d.send(keyMsg("r"))
	d.waitForView("Remove application? Its data will be kept.", 10*time.Second)

	// Toggling to delete-data renames the destruction, and back again.
	d.send(keyMsg("right"))
	d.waitForView("Remove application and delete its data?", 5*time.Second)
	d.send(keyMsg("left"))
	d.waitForView("Remove application? Its data will be kept.", 5*time.Second)

	// Confirm with the keep-data default: the app goes away, the volume stays.
	d.send(keyMsg("tab"))
	d.send(keyMsg("enter"))
	d.waitForView("There are no applications installed", 30*time.Second)
	_, err = docker.FindVolume(ctx, ns, appName)
	assert.NoError(t, err, "keep-data remove should leave the app volume in place")
}

// TestUITailscaleFormEnablesViaControlSeam drives the dashboard's global
// Tailscale settings form (the `t` overlay) end to end: open it, fill the
// credentials, submit, and assert it ran the same enable lifecycle the CLI uses
// - the once-tailscale sidecar and once-docktail boot, the deployed local app
// stays off the tailnet, and once-admin comes up. It also
// checks esc dismissal and that re-opening the form shows the stored settings
// editable once Tailscale is enabled: a re-enable into host mode surfaces the
// pre-flight error, and an unchanged resubmit is an idempotent no-op.
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
	useControlSeam(t, ctx, cp)
	hideHostTailscaled(t)

	startAdminServer(t, ctx)

	deployApp(t, ctx, ns, docker.ApplicationSettings{
		Name:     "whoami",
		Image:    whoamiImage,
		Host:     "whoami.localhost",
		Exposure: docker.ExposureLocal,
	})

	app := ui.NewApp(ns, "")
	d := newAppDriver(t, app)
	d.start()
	d.send(tea.WindowSizeMsg{Width: 120, Height: 40})

	// -- esc dismisses the overlay --
	d.send(keyMsg("t"))
	d.waitForView("Client ID", 5*time.Second)
	d.send(keyMsg("esc"))
	d.waitUntil(func() bool {
		return !strings.Contains(d.viewContent(), "Client ID")
	}, 5*time.Second)

	// -- open the form and enable Tailscale --
	// The credential fields are focused in tab order; the tag is prefilled with
	// tag:once. Under the control seam the credentials are unused: enable skips
	// SaaS OAuth (and with it the credential probe) and boots directly.
	d.send(keyMsg("t"))
	d.waitForView("Client ID", 5*time.Second)
	d.typeText("unused-with-control-seam")
	d.send(keyMsg("tab")) // → Client Secret
	d.typeText("unused-with-control-seam")
	d.send(keyMsg("tab"))   // → Tag (keep the tag:once prefill)
	d.send(keyMsg("tab"))   // → Mode (keep the sidecar default)
	d.send(keyMsg("tab"))   // → Enable
	d.send(keyMsg("enter")) // submit

	// The form runs the real EnableTailscale and stays open (showing progress,
	// which replaces the field labels) until the lifecycle finishes, then closes.
	// Wait on the overlay's "esc close" help, which is present in both the form
	// and progress states but gone once closed. Pump the UI while it runs; on a
	// failure the form stays open with an error and this times out.
	d.waitUntil(func() bool {
		return !strings.Contains(d.viewContent(), "close")
	}, 3*time.Minute)

	// The Docktail topology booted, the sidecar joined the control plane, and
	// the retrofitted app carries the service labels.
	docktailBefore, err := cli.ContainerInspect(ctx, ns.Name()+"-docktail")
	require.NoError(t, err, "once-docktail should be running after enabling via the form")

	// The form always passes what was typed; dropping the credentials the control
	// seam makes unused is the enable lifecycle's job, so what the user typed
	// never reaches the once label. The tag is not dropped.
	stored, err := docker.UnmarshalTailscaleSettings(docktailBefore.Config.Labels["once"])
	require.NoError(t, err)
	assert.Empty(t, stored.ClientID)
	assert.Empty(t, stored.ClientSecret)
	assert.Equal(t, "tag:once", stored.Tag)

	sidecarName := ns.Name() + "-tailscale"
	require.Eventually(t, func() bool {
		found, online, err := cp.nodeOnline(ctx, sidecarName)
		return err == nil && found && online
	}, 90*time.Second, 2*time.Second, "sidecar never joined the control plane after enabling via the form")

	// The local app stays off the tailnet: only tailnet-exposed apps are
	// advertised, and none exist yet.
	assertAppLabels(t, ctx, cli, ns, "whoami", noDocktailLabels)

	_, err = cli.ContainerInspect(ctx, ns.Name()+"-admin")
	require.NoError(t, err, "once-admin should be running after enabling via the form")

	// -- re-opening the form shows the stored settings, editable, with Disable
	// as the secondary action --
	d.send(keyMsg("t"))
	d.waitForView("Tailscale is enabled", 5*time.Second)
	overlay := d.viewContent()
	assert.Contains(t, overlay, "Disable", "disable should stay reachable on the enabled overlay")
	assert.Contains(t, overlay, "Client ID", "the enabled overlay should show the settings fields")
	assert.Contains(t, overlay, "(•) Sidecar", "the stored mode should pre-populate the mode selector")

	// -- re-enabling into host mode fails the pre-flight fast (the host
	// tailscaled is hidden) and surfaces the error in the form --
	d.send(keyMsg("tab"))   // → Client Secret
	d.send(keyMsg("tab"))   // → Tag
	d.send(keyMsg("tab"))   // → Mode
	d.send(keyMsg("space")) // Sidecar → Host
	d.send(keyMsg("tab"))   // → Enable
	d.send(keyMsg("enter")) // submit
	d.waitForView("host mode needs a running tailscaled", 30*time.Second)

	// -- flipping back to sidecar and resubmitting unchanged settings is an
	// idempotent no-op that closes the form without rebuilding the topology --
	d.send(keyMsg("shift+tab")) // → Mode
	d.send(keyMsg("space"))     // Host → Sidecar
	d.send(keyMsg("tab"))       // → Enable
	d.send(keyMsg("enter"))     // submit
	d.waitUntil(func() bool {
		return !strings.Contains(d.viewContent(), "close")
	}, 3*time.Minute)

	docktailAfter, err := cli.ContainerInspect(ctx, ns.Name()+"-docktail")
	require.NoError(t, err)
	assert.Equal(t, docktailBefore.ID, docktailAfter.ID, "an unchanged re-enable must not rebuild once-docktail")

	// -- with Tailscale enabled, a TUI install preselects tailnet with the
	// name-derived service name, and installing advertises the app --
	d.send(keyMsg("n"))
	d.waitForView("Custom Docker image", 5*time.Second)
	d.send(keyMsg("up"))
	d.send(keyMsg("enter"))
	d.waitForView("Image", 5*time.Second)
	d.typeText(whoamiImage)
	d.send(keyMsg("tab"))
	d.send(keyMsg("enter"))

	d.waitForView("Exposure", 5*time.Second)
	view := d.viewContent()
	assert.Contains(t, view, "(•) Tailnet", "tailnet should be preselected when Tailscale is enabled")
	d.send(keyMsg("tab")) // → Host (keep the "whoami" suggestion)
	d.send(keyMsg("tab")) // → Install
	d.send(keyMsg("enter"))

	d.waitUntil(func() bool {
		_ = ns.Refresh(ctx)
		return ns.ApplicationByHost("whoami") != nil
	}, 3*time.Minute)

	tailnetApp := ns.ApplicationByHost("whoami")
	require.Equal(t, docker.ExposureTailnet, tailnetApp.Settings.Exposure)
	assertAppLabels(t, ctx, cli, ns, tailnetApp.Settings.Name, hasDocktailLabels("whoami", ns.Name()))
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
