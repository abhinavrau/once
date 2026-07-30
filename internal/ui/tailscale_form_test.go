package ui

import (
	"errors"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
)

func TestTailscaleForm_EnableFormShowsCredentialFieldsWithPrefilledTag(t *testing.T) {
	m := NewTailscaleForm(newTestNamespace())

	assert.Equal(t, 4, m.form.ItemCount())
	assert.Equal(t, "tag:once", m.form.TextField("tag").Value())
	assert.Equal(t, 0, m.form.RadioField("mode").Selected(), "sidecar should be preselected on a fresh enable")

	view := m.View()
	assert.Contains(t, view, "Client ID")
	assert.Contains(t, view, "Client Secret")
	assert.Contains(t, view, "(•) Sidecar")
	assert.Contains(t, view, "Enable")
	assert.Contains(t, view, "copy policy snippet")
}

func TestTailscaleForm_EnabledFormShowsStoredSettingsWithBothActions(t *testing.T) {
	m := newTailscaleForm(newTestNamespace(), true, docker.TailscaleSettings{
		ClientID:     "K123456CNTRL",
		ClientSecret: "tskey-client-secret",
		Tag:          "tag:custom",
		Mode:         docker.TailscaleModeHost,
	})

	assert.Equal(t, "K123456CNTRL", m.form.TextField("client-id").Value())
	assert.Equal(t, "tskey-client-secret", m.form.TextField("client-secret").Value())
	assert.Equal(t, "tag:custom", m.form.TextField("tag").Value())
	assert.Equal(t, 1, m.form.RadioField("mode").Selected(), "stored host mode should preselect Host")

	view := m.View()
	assert.Contains(t, view, "Tailscale is enabled")
	assert.Contains(t, view, "(•) Host")
	assert.Contains(t, view, "Enable")
	assert.Contains(t, view, "Disable")
	assert.NotContains(t, view, "tskey-client-secret", "the stored secret must stay masked")
}

func TestTailscaleForm_EnabledFormSubmitReenablesWithEditedMode(t *testing.T) {
	m := newTailscaleForm(newTestNamespace(), true, docker.TailscaleSettings{
		ClientID:     "id",
		ClientSecret: "secret",
		Tag:          "tag:once",
		Mode:         docker.TailscaleModeSidecar,
	})
	m.form.RadioField("mode").SetSelected(1)

	msg := submitAfterTabs(t, m.form, m.form.ItemCount())
	assert.Equal(t, tailscaleEnableSubmitMsg{id: "id", secret: "secret", tag: "tag:once", mode: docker.TailscaleModeHost}, msg)
}

func TestTailscaleForm_EnabledFormDisableActionWorksWithEmptySeamSettings(t *testing.T) {
	// Under the headscale control seam no credentials or tag are stored; the
	// fields must not be required, or neither action could ever submit.
	m := newTailscaleForm(newTestNamespace(), true, docker.TailscaleSettings{Mode: docker.TailscaleModeSidecar})

	msg := submitAfterTabs(t, m.form, m.form.ItemCount()+1) // fields, Enable, → Disable action
	assert.IsType(t, tailscaleDisableSubmitMsg{}, msg)
}

func TestTailscaleForm_SubmitCarriesSelectedMode(t *testing.T) {
	submitMode := func(selected int) string {
		m := NewTailscaleForm(newTestNamespace())
		m.form.TextField("client-id").SetValue("id")
		m.form.TextField("client-secret").SetValue("secret")
		m.form.RadioField("mode").SetSelected(selected)

		msg, ok := submitAfterTabs(t, m.form, m.form.ItemCount()).(tailscaleEnableSubmitMsg)
		require.True(t, ok)
		return msg.mode
	}

	assert.Equal(t, docker.TailscaleModeSidecar, submitMode(0))
	assert.Equal(t, docker.TailscaleModeHost, submitMode(1))
}

func TestTailscaleForm_EnableSubmitRunsInBackground(t *testing.T) {
	m := NewTailscaleForm(newTestNamespace())

	c, cmd := m.Update(tailscaleEnableSubmitMsg{id: "id", secret: "secret", tag: "tag:once"})

	got := c.(TailscaleForm)
	assert.True(t, got.running)
	assert.NotNil(t, cmd)
}

func TestTailscaleForm_StatusShowsWhileRunningAndKeepsListening(t *testing.T) {
	m := NewTailscaleForm(newTestNamespace())
	m.running = true
	m.statusChan = make(chan string, 1)

	c, cmd := m.Update(tailscaleStatusMsg("waiting up to 1m30s for the tailnet to approve: whoami"))

	got := c.(TailscaleForm)
	assert.Contains(t, got.View(), "waiting up to 1m30s")
	require.NotNil(t, cmd, "the form must keep listening for the next status")

	got.statusChan <- "next status"
	assert.Equal(t, tailscaleStatusMsg("next status"), cmd())

	// The channel closing (task finished) ends the relay without a message.
	close(got.statusChan)
	_, cmd = got.Update(tailscaleStatusMsg("late status"))
	assert.Nil(t, cmd())
}

func TestTailscaleForm_CopyKeyCopiesSnippetForEnteredTag(t *testing.T) {
	m := NewTailscaleForm(newTestNamespace())

	_, cmd := m.Update(keyPressMsg("ctrl+p"))
	assert.NotNil(t, cmd)
}

func TestTailscaleForm_FinishedErrorStaysOpen(t *testing.T) {
	m := NewTailscaleForm(newTestNamespace())
	m.running = true

	c, _ := m.Update(tailscaleFormFinishedMsg{err: errors.New("probe failed")})

	got := c.(TailscaleForm)
	require.Error(t, got.err)
	assert.False(t, got.running)
	assert.Contains(t, got.View(), "probe failed")
}

func TestTailscaleForm_TagPolicyErrorPointsAtSnippet(t *testing.T) {
	m := NewTailscaleForm(newTestNamespace())
	m.running = true

	c, _ := m.Update(tailscaleFormFinishedMsg{err: fmt.Errorf("probe: %w", docker.ErrTagNotInPolicy)})

	got := c.(TailscaleForm)
	assert.Contains(t, got.View(), "ctrl+p copies the required policy snippet")
}

func TestTailscaleForm_FinishedSuccessCloses(t *testing.T) {
	m := NewTailscaleForm(newTestNamespace())
	m.running = true

	_, cmd := m.Update(tailscaleFormFinishedMsg{})

	require.NotNil(t, cmd)
	assert.IsType(t, TailscaleFormCloseMsg{}, cmd())
}

// Helpers

// submitAfterTabs tabs through the form and presses enter, returning the
// message the landed-on button emits.
func submitAfterTabs(t *testing.T, f Form, tabs int) tea.Msg {
	t.Helper()
	for range tabs {
		f, _ = f.Update(keyPressMsg("tab"))
	}
	_, cmd := f.Update(keyPressMsg("enter"))
	require.NotNil(t, cmd)
	return cmd()
}
