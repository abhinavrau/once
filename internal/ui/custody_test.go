package ui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
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

// The reveal screen must fit the 80x24 terminal floor with a representative
// kit - an offsite URL and two credential names.
func TestCustodyRevealFitsTerminalFloor(t *testing.T) {
	c := NewCustody(testRecoveryKit())
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
	msgs := custodySubmitGate(t, &c)

	assert.Contains(t, msgs, tea.Msg(CustodyResultMsg{Confirmed: true}))
	assert.Contains(t, c.View(), "backups are on")

	closed := custodyPressKey(t, &c, "enter")
	assert.Contains(t, closed, tea.Msg(CustodyClosedMsg{Confirmed: true}))
}

func TestCustodyGateRejectsMismatch(t *testing.T) {
	c := custodyAtGate(t)

	c.gate.TextField("password").SetValue("wrong")
	msgs := custodySubmitGate(t, &c)

	assert.NotContains(t, msgs, tea.Msg(CustodyResultMsg{Confirmed: true}))
	assert.Contains(t, c.View(), "That doesn't match")
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
	msgs := custodyPressKey(t, &c, "s")
	assert.Contains(t, msgs, tea.Msg(CustodyResultMsg{Confirmed: false}))
	assert.Contains(t, c.View(), "NOT enabled")

	closed := custodyPressKey(t, &c, "enter")
	assert.Contains(t, closed, tea.Msg(CustodyClosedMsg{Confirmed: false}))
}

func TestCustodyCopyKeysOnReveal(t *testing.T) {
	c := NewCustody(testRecoveryKit())
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
	c := NewCustody(testRecoveryKit())
	c, _ = c.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	assert.True(t, c.ShowingSecret())

	custodyPressKey(t, &c, "enter")
	assert.False(t, c.ShowingSecret())
}

// Helpers

func custodyAtGate(t *testing.T) Custody {
	t.Helper()
	c := NewCustody(testRecoveryKit())
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
