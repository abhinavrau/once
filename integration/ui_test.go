package integration

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
	"github.com/basecamp/once/internal/mouse"
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
	bootProxy(t, ctx, ns, proxyPorts)

	app := ui.NewApp(ns, "")
	d := newAppDriver(t, app)
	d.start()

	d.send(tea.WindowSizeMsg{Width: 120, Height: 40})

	// -- Screen 1: App list → select "Custom Docker image" --
	d.send(keyMsg("up"))
	d.send(keyMsg("enter"))

	d.waitForView("Image", 5*time.Second)

	// -- Screen 2: Image form --
	d.fillField("image", "ghcr.io/basecamp/once-campfire:main")
	d.click("submit")

	d.waitForView("Exposure", 5*time.Second)

	// -- Screen 3: Exposure form: local preselected (Tailscale is off) with
	// the name-derived host suggestion prefilled; accept both. A custom image
	// is asked the data path, the health check path (unconditionally), and
	// the backup participation checkbox. --
	view := d.viewContent()
	assert.Contains(t, view, "(•) Local", "local should be preselected without Tailscale")
	assert.Contains(t, view, "once-campfire.localhost", "host should prefill the derived suggestion")
	assert.Contains(t, view, "Data path", "a custom image is asked its data path")
	assert.Contains(t, view, "Health check path", "a custom image is asked its health path unconditionally")
	assert.Contains(t, view, "Include this app in backups")

	// -- Selecting tailnet exposure while Tailscale is disabled offers the
	// enable overlay inline; dismissing it returns to the filled-in form. --
	d.selectRadioOption("exposure", "Tailnet")
	d.click("submit")
	d.waitForView("tagOwners", 5*time.Second)
	d.send(keyMsg("esc"))
	d.waitUntil(func() bool {
		return !strings.Contains(d.viewContent(), "tagOwners")
	}, 5*time.Second)

	// Back on the filled-in form: return to local (which restores the local
	// host suggestion) and install.
	d.selectRadioOption("exposure", "Local")
	assert.Contains(t, d.viewContent(), "once-campfire.localhost")
	d.click("submit")

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
	d.click("confirm")
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
	bootProxy(t, ctx, ns, getProxyPorts(t))
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
	// The enable form with a Mode radio runs to ~46 rows; the overlay clips
	// what does not fit, so give it room and assert the whole form rendered.
	d.send(tea.WindowSizeMsg{Width: 120, Height: 50})

	// -- esc dismisses the overlay (a fresh enable opens on the tagOwners
	// screen, ADR-0017 phase one) --
	d.send(keyMsg("t"))
	d.waitForView("tagOwners", 5*time.Second)
	d.send(keyMsg("esc"))
	d.waitUntil(func() bool {
		return !strings.Contains(d.viewContent(), "tagOwners")
	}, 5*time.Second)

	// -- open the form and enable Tailscale --
	// The tag keeps its tag:once prefill and the mode its sidecar default.
	// Under the control seam the credentials are unused: enable skips SaaS OAuth
	// (and with it the credential probe) and boots directly. This test exercises
	// the Services topology's own behavior (mode host/sidecar, once-docktail),
	// so it selects Services explicitly - nodes is the fresh-enable default and
	// has no Mode field at all.
	d.send(keyMsg("t"))
	d.waitForView("tagOwners", 5*time.Second)
	// The copy controls are named click targets; clicking one flips its
	// Copied state and Continue advances to the credential form.
	d.click("copy-block")
	require.Contains(t, d.viewContent(), "Copied", "the copy control must confirm the copy")
	d.click("continue")
	d.waitForView("Client ID", 5*time.Second)
	d.selectRadioOption("topology", "Services")
	require.Contains(t, d.viewContent(), "(•) Sidecar", "selecting Services should reveal the Mode field")
	d.fillField("client-id", "unused-with-control-seam")
	d.fillField("client-secret", "unused-with-control-seam")
	require.Contains(t, d.viewContent(), "close", "the form's help must be visible: an overlay taller than the window is clipped")
	d.click("submit")

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
	d.selectRadioOption("mode", "Host")
	d.click("submit")
	d.waitForView("host mode needs a running tailscaled", 30*time.Second)

	// -- flipping back to sidecar and resubmitting unchanged settings is an
	// idempotent no-op that closes the form without rebuilding the topology --
	d.selectRadioOption("mode", "Sidecar")
	d.click("submit")
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
	d.fillField("image", whoamiImage)
	d.click("submit")

	d.waitForView("Exposure", 5*time.Second)
	view := d.viewContent()
	assert.Contains(t, view, "(•) Tailnet", "tailnet should be preselected when Tailscale is enabled")
	// Keep the "whoami" service-name suggestion and install.
	d.click("submit")

	d.waitUntil(func() bool {
		_ = ns.Refresh(ctx)
		return ns.ApplicationByHost("whoami") != nil
	}, 3*time.Minute)

	tailnetApp := ns.ApplicationByHost("whoami")
	require.Equal(t, docker.ExposureTailnet, tailnetApp.Settings.Exposure)
	assertAppLabels(t, ctx, cli, ns, tailnetApp.Settings.Name, hasDocktailLabels("whoami", ns.Name()))
}

// TestUIBackupsOverlayEnableFlow drives the dashboard's global Backups
// overlay (the `b` key) through the full enable sequence: fill the local
// path, submit, wait out the validation probe, then the custody moment -
// recovery kit reveal, type-back gate fed a lowercase dashed password (the
// gate normalizes) - and assert Enabled was persisted and the probe left no
// snapshot behind.
func TestUIBackupsOverlayEnableFlow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	ns, err := docker.NewNamespace("once-backups-ui-test")
	require.NoError(t, err)
	t.Cleanup(func() { ns.Teardown(context.Background(), true) })

	require.NoError(t, ns.EnsureNetwork(ctx))
	bootProxy(t, ctx, ns, getProxyPorts(t))

	deployApp(t, ctx, ns, docker.ApplicationSettings{
		Name:     "whoami",
		Image:    whoamiImage,
		Host:     "whoami.localhost",
		Exposure: docker.ExposureLocal,
	})

	repoDir := filepath.Join(t.TempDir(), "backups")
	t.Cleanup(func() { chownToTestUser(t, repoDir) })

	app := ui.NewApp(ns, "")
	d := newAppDriver(t, app)
	d.start()
	d.send(tea.WindowSizeMsg{Width: 120, Height: 40})

	// -- esc dismisses the overlay --
	d.send(keyMsg("b"))
	d.waitForView("Local repository path", 5*time.Second)
	d.send(keyMsg("esc"))
	d.waitUntil(func() bool {
		return !strings.Contains(d.viewContent(), "Local repository path")
	}, 5*time.Second)

	// -- point the local path at a fresh directory and submit: the probe
	// runs (containerized restic against the real repo), then the custody
	// reveal renders the recovery kit --
	d.send(keyMsg("b"))
	d.waitForView("Local repository path", 5*time.Second)
	d.clearField("local-path")
	d.typeText(repoDir)
	d.click("submit")

	// The probe runs, then custody asks how to take custody. Both deliveries
	// are offered here; this flow takes the screen.
	d.waitForView("Send it to my phone", 5*time.Minute)
	d.click("delivery:screen")
	d.waitForView("ONCE RECOVERY KIT", 10*time.Second)

	password := ns.BackupRepoSettings().Password
	require.Regexp(t, `^[A-Z2-7]{32}$`, password, "the probe persisted the generated password")
	require.False(t, ns.BackupRepoSettings().Enabled, "Enabled is withheld until the gate passes")

	// -- the gate normalizes case, dashes, and spaces --
	d.send(keyMsg("enter"))
	d.waitForView("Prove it's saved", 5*time.Second)
	d.fillField("password", strings.ToLower(docker.FormatRepoPassword(password)))
	d.click("submit")
	d.waitForView("backups are on", 10*time.Second)

	// -- closing the done screen closes the overlay; Enabled is persisted --
	d.send(keyMsg("enter"))
	d.waitUntil(func() bool {
		return !strings.Contains(d.viewContent(), "backups are on")
	}, 10*time.Second)
	d.waitUntil(func() bool {
		_ = ns.Refresh(ctx)
		return ns.BackupRepoSettings().Enabled
	}, 30*time.Second)

	assert.Empty(t, resticSnapshotGroups(t, ctx, repoDir, password),
		"the probe leaves no snapshot behind")
}

// TestUIInstallBackupCheckboxOffersOverlayOnce covers the install flow's
// participation checkbox: ticking it with no Backup Repo configured opens
// the install-wide overlay inline exactly once, and the catalog path asks
// the same checkbox without the custom-image profile fields.
func TestUIInstallBackupCheckboxOffersOverlayOnce(t *testing.T) {
	ns, err := docker.NewNamespace("once-backup-checkbox-test")
	require.NoError(t, err)
	t.Cleanup(func() { ns.Teardown(context.Background(), true) })

	app := ui.NewApp(ns, "")
	d := newAppDriver(t, app)
	d.start()
	d.send(tea.WindowSizeMsg{Width: 120, Height: 40})

	// -- custom image path --
	d.send(keyMsg("up"))
	d.send(keyMsg("enter"))
	d.waitForView("Image", 5*time.Second)
	d.fillField("image", whoamiImage)
	d.click("submit")
	d.waitForView("Exposure", 5*time.Second)

	// Ticking the checkbox with no repo configured opens the overlay inline.
	d.clickField("auto-backup")
	d.waitForView("Local repository path", 5*time.Second)

	// Dismissing returns to the filled-in form, checkbox still ticked.
	d.send(keyMsg("esc"))
	d.waitUntil(func() bool {
		return !strings.Contains(d.viewContent(), "Local repository path")
	}, 5*time.Second)
	assert.Contains(t, d.viewContent(), "[✓] Include this app in backups")

	// The overlay is offered once, not on every toggle.
	d.clickField("auto-backup")
	d.clickField("auto-backup")
	d.flush()
	assert.NotContains(t, d.viewContent(), "Local repository path",
		"the inline overlay opens once, not on every tick")

	// -- catalog path asks the checkbox but not the profile fields --
	d.send(keyMsg("esc"))
	d.waitForView("Image", 5*time.Second)
	d.send(keyMsg("esc"))
	d.waitForView("Custom Docker image", 5*time.Second)
	// The cursor is still on "Custom Docker image"; wrap down to the first
	// catalog entry.
	d.send(keyMsg("down"))
	d.send(keyMsg("enter"))
	d.waitForView("Exposure", 5*time.Second)
	view := d.viewContent()
	assert.Contains(t, view, "Include this app in backups")
	assert.NotContains(t, view, "Data path", "catalog apps carry their profile already")
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

// click enters the update chain where a real click does once App has resolved
// coordinates to a target name: App hands the event to the current screen, the
// screen to any open overlay, and the component resolves the target itself.
// It fails the test immediately, naming the target, if nothing on the current
// screen is registered under that name - otherwise the click would silently
// no-op and leave the driver typing into whatever was already focused.
func (d *appDriver) click(target string) {
	d.t.Helper()
	view := d.viewContent()
	if !mouse.Registered(target) {
		d.t.Fatalf("click target %q is not registered on the current screen\n\nView:\n%s", target, view)
	}
	d.send(ui.MouseEvent{Target: target, IsClick: true})
}

// clickField clicks the form field with the given key. On a text field that
// only moves focus; on a checkbox it toggles, and on a radio it cycles one
// option - the same as pressing space with the field focused.
func (d *appDriver) clickField(key string) {
	d.click("field:" + key)
}

func (d *appDriver) fillField(key, text string) {
	d.clickField(key)
	d.typeText(text)
}

// clearField focuses the keyed text field and kills any prefilled value
// (ctrl+u is textinput's delete-before-cursor; the cursor sits at the end).
func (d *appDriver) clearField(key string) {
	d.clickField(key)
	d.send(tea.KeyPressMsg(tea.Key{Code: 'u', Mod: tea.ModCtrl}))
}

// maxRadioClicks bounds selectRadioOption so a label that never appears fails
// with a view dump instead of spinning.
const maxRadioClicks = 8

// selectRadioOption clicks the keyed radio field until label is the selected
// option. One click cycles one option, so a full cycle is the worst case.
func (d *appDriver) selectRadioOption(key, label string) {
	d.t.Helper()
	selected := func() bool { return strings.Contains(d.viewContent(), "(•) "+label) }
	for range maxRadioClicks {
		if selected() {
			return
		}
		d.clickField(key)
	}
	if !selected() {
		d.t.Fatalf("radio %q never selected %q\n\nView:\n%s", key, label, d.viewContent())
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

// processMsg runs on the test goroutine, so a rejected submit can fail the test
// on the spot. The rejection rides a tea.Batch alongside the submitting form's
// own command and the two are unordered, so this only assumes the message
// arrives, not when.
func (d *appDriver) processMsg(msg tea.Msg) {
	d.t.Helper()
	switch msg := msg.(type) {
	case tea.BatchMsg:
		for _, cmd := range msg {
			d.enqueueCmd(cmd)
		}
		return
	case ui.FormSubmitRejectedMsg:
		d.t.Fatalf("form rejected the submit on field %q: %s\n\nView:\n%s",
			msg.Key, msg.Reason, d.viewContent())
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
