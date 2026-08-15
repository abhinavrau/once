package ui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
	"github.com/basecamp/once/internal/mouse"
)

func testRecoveryKit() RecoveryKit {
	return RecoveryKit{
		Password:        "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567",
		LocalPath:       "/var/lib/once/backups",
		OffsiteURL:      "s3:s3.eu-central-1.amazonaws.com/my-bucket/once",
		CredentialNames: []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"},
		CreatedAt:       time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC),
		Hostname:        "myserver",
	}
}

// The kit is built from the settings the enable flow has just persisted, and
// it is everything the operator carries off the machine: lose a fact here and
// a recovered host cannot open the repo.
func TestNewRecoveryKitCarriesTheSettings(t *testing.T) {
	kit := NewRecoveryKit(docker.BackupRepoSettings{
		Enabled:    true,
		LocalPath:  "/var/lib/once/backups",
		OffsiteURL: "s3:s3.eu-central-1.amazonaws.com/my-bucket/once",
		OffsiteEnv: map[string]string{"AWS_SECRET_ACCESS_KEY": "s3cr3t-value", "AWS_ACCESS_KEY_ID": "AKIA-value"},
		Password:   "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567",
	})

	assert.Equal(t, "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567", kit.Password)
	assert.Equal(t, "/var/lib/once/backups", kit.LocalPath)
	assert.Equal(t, "s3:s3.eu-central-1.amazonaws.com/my-bucket/once", kit.OffsiteURL)
	assert.Equal(t, []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"}, kit.CredentialNames,
		"sorted, so two runs of the same install read alike")
	assert.WithinDuration(t, time.Now(), kit.CreatedAt, time.Minute)
	assert.NotEmpty(t, kit.Hostname)

	text := kit.Text()
	assert.Contains(t, text, docker.FormatRepoPassword(kit.Password))
	assert.Contains(t, text, "/var/lib/once/backups")
	assert.NotContains(t, text, "s3cr3t-value", "credential values stay in Once's environment; the kit names them only")
	assert.NotContains(t, text, "AKIA-value")
}

// The reveal screen must fit the 80x24 terminal floor with a representative
// kit - an offsite URL and two credential names.
func TestCustodyRevealFitsTerminalFloor(t *testing.T) {
	c := newScreenCustody(t)
	c, _ = c.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	require.True(t, c.ShowingSecret(), "one delivery option collapses the choice prompt straight to the reveal")
	view := c.View()
	assert.LessOrEqual(t, lipgloss.Height(view), 24, "reveal must fit 24 rows:\n%s", view)
	assert.LessOrEqual(t, lipgloss.Width(view), 80, "reveal must fit 80 columns:\n%s", view)
	assert.Contains(t, view, "ONCE RECOVERY KIT")
	assert.Contains(t, view, docker.FormatRepoPassword(c.kit.Password))
}

func TestRecoveryKitTextOmitsUnconfiguredOffsite(t *testing.T) {
	kit := testRecoveryKit()
	kit.OffsiteURL = ""
	kit.CredentialNames = nil

	text := kit.Text()
	assert.NotContains(t, text, "Offsite")
	assert.NotContains(t, text, "Credential")
	assert.Contains(t, text, docker.FormatRepoPassword(kit.Password))
	assert.Contains(t, text, kit.LocalPath)
}

// The gate normalizes case, dashes, and spaces - what display formatting and
// phone keyboards introduce - before comparing.
func TestCustodyGateNormalizesTypedPassword(t *testing.T) {
	c := custodyAtGate(t)

	c.gate.TextField("password").SetValue("abcd-efgh ijkl mnop-qrst-uvwx-yz23-4567")
	custodySubmitGate(t, &c)

	assert.Contains(t, c.View(), "backups are on")

	closed := custodyPressKey(t, &c, "enter")
	assert.Contains(t, closed, tea.Msg(CustodyClosedMsg{Confirmed: true}))
}

func TestCustodyGateRejectsMismatch(t *testing.T) {
	c := custodyAtGate(t)

	c.gate.TextField("password").SetValue("wrong")
	custodySubmitGate(t, &c)

	assert.Contains(t, c.View(), "That doesn't match")
	custodyPressKey(t, &c, "enter")
	assert.NotContains(t, c.View(), "backups are on", "a mismatch never reaches the done screen")
}

// esc opens a loud skip confirm; skipping reports an unconfirmed result and
// enter on the confirm returns to the gate instead.
func TestCustodySkipHatch(t *testing.T) {
	c := custodyAtGate(t)

	custodyPressKey(t, &c, "esc")
	assert.Contains(t, c.View(), "Skip without confirming?")

	custodyPressKey(t, &c, "enter")
	assert.Contains(t, c.View(), "Prove it's saved", "enter returns to the gate")

	custodyPressKey(t, &c, "esc")
	custodyPressKey(t, &c, "s")
	assert.Contains(t, c.View(), "NOT enabled")

	closed := custodyPressKey(t, &c, "enter")
	assert.Contains(t, closed, tea.Msg(CustodyClosedMsg{Confirmed: false}))
}

func TestCustodyCopyKeysOnReveal(t *testing.T) {
	c := newScreenCustody(t)
	c, _ = c.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	var cmd tea.Cmd
	c, cmd = c.Update(keyPressMsg("ctrl+p"))
	assert.NotNil(t, cmd, "ctrl+p copies the password")
	assert.Contains(t, c.View(), "password copied")

	c, cmd = c.Update(keyPressMsg("ctrl+k"))
	assert.NotNil(t, cmd, "ctrl+k copies the whole kit")
	assert.Contains(t, c.View(), "kit copied")
}

// The gate keeps mouse tracking (click-to-focus); the reveal opts out so
// native terminal selection can copy the secret.
func TestCustodyShowingSecretOnlyOnReveal(t *testing.T) {
	c := newScreenCustody(t)
	c, _ = c.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	assert.True(t, c.ShowingSecret())

	custodyPressKey(t, &c, "enter")
	assert.False(t, c.ShowingSecret())
}

// The phone handoff appears as a second option only when there is somewhere to
// publish it and a terminal that can draw a code worth scanning.
func TestCustodyOffersThePhoneOnlyWhenItCanBeScanned(t *testing.T) {
	c := newQRCustody(t, &fakeHandoff{url: testCustodyQRURL})
	require.Equal(t, custodyStepChoice, c.step, "two options bring the prompt back")
	assert.Contains(t, c.View(), "Show it on this screen")
	assert.Contains(t, c.View(), "Send it to my phone")

	// Nothing to publish to.
	none := NewCustody(testRecoveryKit(), nil, CustodyDeliveryAsk)
	assert.Len(t, none.deliveries, 1)
	assert.Equal(t, custodyStepReveal, none.step, "one option collapses the prompt")

	// A terminal that would mangle the code.
	withColorProfile(t, colorprofile.Ascii)
	degraded := NewCustody(testRecoveryKit(), &fakeHandoff{url: testCustodyQRURL}, CustodyDeliveryAsk)
	assert.Len(t, degraded.deliveries, 1)
	assert.Equal(t, custodyStepReveal, degraded.step)
}

// The intro is a screen of its own, and it must be passed through: the
// same-network requirement, the single-use expiry and the certificate warning
// have to be read before anything is scannable.
func TestCustodyQRIntroPrecedesTheCode(t *testing.T) {
	handoff := &fakeHandoff{url: testCustodyQRURL}
	c := newQRCustody(t, handoff)

	custodyPressKey(t, &c, "down")
	custodyPressKey(t, &c, "enter")

	require.Equal(t, custodyStepQRIntro, c.step)
	intro := c.View()
	assert.Contains(t, intro, "same network")
	assert.Contains(t, intro, "expires after 10 minutes")
	assert.Contains(t, intro, "warn about the certificate")
	assert.Contains(t, intro, "blank")
	assert.Equal(t, 0, handoff.started, "nothing is published until the intro is read")

	custodyPressKey(t, &c, "enter")
	assert.Equal(t, 1, handoff.started)
	assert.Contains(t, c.View(), testCustodyQRURL)
	assert.Contains(t, c.View(), qrHalfBlock)
}

func TestCustodyQRViewFitsTerminalFloor(t *testing.T) {
	for _, url := range []string{testCustodyQRURL, "https://once-admin.tail9dc1e2.ts.net/r/K3PZQ7XW4A2BCDEF"} {
		c := custodyAtQR(t, &fakeHandoff{url: url})
		view := c.View()
		assert.LessOrEqual(t, lipgloss.Height(view), 24, "the code must fit 24 rows: %s", url)
		assert.LessOrEqual(t, lipgloss.Width(view), 80, "the code must fit 80 columns: %s", url)
	}
}

func TestCustodyQRPathGatesAndTearsTheHandoffDown(t *testing.T) {
	handoff := &fakeHandoff{url: testCustodyQRURL}
	c := custodyAtQR(t, handoff)

	custodyPressKey(t, &c, "enter")
	require.Equal(t, custodyStepGate, c.step)
	assert.Contains(t, c.View(), "the copy on your phone", "the gate names where the password now is")

	c.gate.TextField("password").SetValue(docker.FormatRepoPassword(testRecoveryKit().Password))
	custodySubmitGate(t, &c)

	assert.Contains(t, c.View(), "backups are on")
	assert.Equal(t, 1, handoff.stopped, "the page comes down as soon as the gate passes")
}

func TestCustodySkippingAlsoTearsTheHandoffDown(t *testing.T) {
	handoff := &fakeHandoff{url: testCustodyQRURL}
	c := custodyAtQR(t, handoff)

	custodyPressKey(t, &c, "enter")
	custodyPressKey(t, &c, "esc")
	custodyPressKey(t, &c, "s")

	assert.Contains(t, c.View(), "NOT enabled")
	assert.Equal(t, 1, handoff.stopped)
}

func TestCustodyQRSurfacesAFailureToPublish(t *testing.T) {
	c := custodyAtQR(t, &fakeHandoff{err: errors.New("no local network address found")})

	view := c.View()
	assert.Contains(t, view, "Could not serve the page")
	assert.Contains(t, view, "no local network address found")
	assert.Contains(t, view, "show it on this screen instead")

	// Enter must not advance to the gate from a screen that showed no code.
	custodyPressKey(t, &c, "enter")
	assert.Equal(t, custodyStepQR, c.step)
}

func TestCustodyPreselectedDeliverySkipsThePrompt(t *testing.T) {
	withColorProfile(t, colorprofile.TrueColor)
	t.Setenv("LC_ALL", "en_US.UTF-8")
	handoff := &fakeHandoff{url: testCustodyQRURL}

	qr := NewCustody(testRecoveryKit(), handoff, CustodyDeliveryQR)
	assert.Equal(t, custodyStepQRIntro, qr.step)

	screen := NewCustody(testRecoveryKit(), handoff, CustodyDeliveryScreen)
	assert.Equal(t, custodyStepReveal, screen.step)
}

func TestCustodyChoiceIsClickable(t *testing.T) {
	c := newQRCustody(t, &fakeHandoff{url: testCustodyQRURL})
	// The view registers the targets the UI-test driver clicks.
	mouse.Sweep(c.View())
	require.True(t, mouse.Registered(custodyDeliveryTarget(CustodyDeliveryQR)))

	c, _ = c.Update(MouseEvent{Target: custodyDeliveryTarget(CustodyDeliveryQR), IsClick: true})
	assert.Equal(t, custodyStepQRIntro, c.step)
}

// The page carries the password in a field of its own, so it must not also
// appear in the details box beside it - where no password manager would find
// it, and where it would sit in plain sight.
func TestPageKitKeepsThePasswordOutOfTheDetails(t *testing.T) {
	kit := testRecoveryKit()
	page := kit.PageKit()

	assert.Equal(t, kit.Password, page.Password)
	require.NotEmpty(t, page.Details)
	for _, detail := range page.Details {
		assert.NotContains(t, detail.Value, kit.Password)
		assert.NotContains(t, detail.Value, docker.FormatRepoPassword(kit.Password))
	}
	assert.Len(t, page.Details, len(kit.rows())-1, "every row but the secret one crosses over")
}

// Helpers

const testCustodyQRURL = "https://192.168.100.250/r/K3PZQ7XW4A2BCDEF"

type fakeHandoff struct {
	url              string
	err              error
	started, stopped int
}

func (f *fakeHandoff) Start(context.Context, RecoveryKit) (string, error) {
	f.started++
	return f.url, f.err
}

func (f *fakeHandoff) Stop(context.Context) error {
	f.stopped++
	return nil
}

func withColorProfile(t *testing.T, profile colorprofile.Profile) {
	t.Helper()
	previous := ColorProfile()
	t.Cleanup(func() { SetColorProfile(previous) })
	SetColorProfile(profile)
}

// newScreenCustody is the flow with no phone delivery available, which
// collapses the prompt straight to the reveal.
func newScreenCustody(t *testing.T) Custody {
	t.Helper()
	return NewCustody(testRecoveryKit(), nil, CustodyDeliveryAsk)
}

// newQRCustody pins what decides whether the QR is offered, so the test does
// not depend on the developer's own terminal or locale.
func newQRCustody(t *testing.T, handoff CustodyHandoff) Custody {
	t.Helper()
	withColorProfile(t, colorprofile.TrueColor)
	t.Setenv("LC_ALL", "en_US.UTF-8")

	c := NewCustody(testRecoveryKit(), handoff, CustodyDeliveryAsk)
	c, _ = c.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return c
}

// custodyAtQR walks the flow to the code itself.
func custodyAtQR(t *testing.T, handoff CustodyHandoff) Custody {
	t.Helper()
	withColorProfile(t, colorprofile.TrueColor)
	t.Setenv("LC_ALL", "en_US.UTF-8")

	c := NewCustody(testRecoveryKit(), handoff, CustodyDeliveryQR)
	c, _ = c.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	require.Equal(t, custodyStepQRIntro, c.step)
	custodyPressKey(t, &c, "enter")
	require.Equal(t, custodyStepQR, c.step)
	return c
}

func custodyAtGate(t *testing.T) Custody {
	t.Helper()
	c := newScreenCustody(t)
	c, _ = c.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	custodyPressKey(t, &c, "enter")
	require.Contains(t, c.View(), "Prove it's saved")
	return c
}

// custodyPressKey sends one key and pumps every resulting message back into
// the component, returning all messages produced along the way.
func custodyPressKey(t *testing.T, c *Custody, k string) []tea.Msg {
	t.Helper()
	return pumpCustody(t, c, keyPressMsg(k))
}

// custodySubmitGate clicks the gate's submit button and pumps the outcome.
func custodySubmitGate(t *testing.T, c *Custody) []tea.Msg {
	t.Helper()
	return pumpCustody(t, c, MouseEvent{Target: "submit", IsClick: true})
}

// pumpCustody feeds msg to the component and re-delivers only this module's
// own messages. External ones (cursor blinks, clipboard acks) are dropped:
// the blink command reschedules itself forever, so following it would never
// terminate.
func pumpCustody(t *testing.T, c *Custody, msg tea.Msg) []tea.Msg {
	t.Helper()
	var seen []tea.Msg
	var cmd tea.Cmd
	*c, cmd = c.Update(msg)
	for _, produced := range collectMsgs(cmd) {
		if !isOnceMsg(produced) {
			continue
		}
		seen = append(seen, produced)
		seen = append(seen, pumpCustody(t, c, produced)...)
	}
	return seen
}

func collectMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var msgs []tea.Msg
		for _, c := range batch {
			msgs = append(msgs, collectMsgs(c)...)
		}
		return msgs
	}
	return []tea.Msg{msg}
}

func isOnceMsg(msg tea.Msg) bool {
	return strings.HasPrefix(reflect.TypeOf(msg).PkgPath(), "github.com/basecamp/once/")
}
