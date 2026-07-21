package ui

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
)

func TestTailscaleForm_EnableFormShowsCredentialFieldsWithPrefilledTag(t *testing.T) {
	m := NewTailscaleForm(newTestNamespace())

	assert.Equal(t, 4, m.form.ItemCount())
	assert.Equal(t, "tag:once", m.form.TextField(tailscaleTagField).Value())
	assert.Equal(t, 0, m.form.RadioField(tailscaleModeField).Selected(), "sidecar should be preselected on a fresh enable")

	view := m.View()
	assert.Contains(t, view, "Client ID")
	assert.Contains(t, view, "Client Secret")
	assert.Contains(t, view, "(•) Sidecar")
	assert.Contains(t, view, "Enable")
	assert.Contains(t, view, "copy policy snippet")
}

func TestTailscaleForm_SubmitCarriesSelectedMode(t *testing.T) {
	submitMode := func(selected int) string {
		m := NewTailscaleForm(newTestNamespace())
		m.form.TextField(tailscaleClientIDField).SetValue("id")
		m.form.TextField(tailscaleClientSecretField).SetValue("secret")
		m.form.RadioField(tailscaleModeField).SetSelected(selected)

		f := m.form
		for range f.ItemCount() {
			f, _ = f.Update(keyPressMsg("tab"))
		}
		_, cmd := f.Update(keyPressMsg("enter"))
		require.NotNil(t, cmd)
		msg, ok := cmd().(tailscaleEnableSubmitMsg)
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
