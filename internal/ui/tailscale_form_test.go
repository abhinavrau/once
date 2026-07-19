package ui

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
)

func TestTailscaleForm_EnableFormShowsBootstrapFieldsWithPrefilledTag(t *testing.T) {
	m := NewTailscaleForm(newTestNamespace())

	assert.Equal(t, 3, m.form.ItemCount())
	assert.Equal(t, "tag:once", m.form.TextField(tailscaleTagField).Value())

	view := m.View()
	assert.Contains(t, view, "Bootstrap Client ID")
	assert.Contains(t, view, "Bootstrap Client Secret")
	assert.Contains(t, view, "Enable")
}

func TestTailscaleForm_EnableSubmitRunsInBackground(t *testing.T) {
	m := NewTailscaleForm(newTestNamespace())

	c, cmd := m.Update(tailscaleEnableSubmitMsg{id: "id", secret: "secret", tag: "tag:once"})

	got := c.(TailscaleForm)
	assert.True(t, got.running)
	assert.Equal(t, "tag:once", got.tag)
	assert.NotNil(t, cmd)
}

func TestTailscaleForm_ConsentShowsDiffWithApplyAndDecline(t *testing.T) {
	m := NewTailscaleForm(newTestNamespace())
	m.running = true

	c, _ := m.Update(tailscaleConsentMsg{policyClientID: "k123", update: &docker.PolicyUpdate{}})

	got := c.(TailscaleForm)
	assert.Equal(t, tailscaleFormStateConsent, got.state)
	assert.False(t, got.running)

	view := got.View()
	assert.Contains(t, view, "GitOps")
	assert.Contains(t, view, "Apply")
	assert.Contains(t, view, "Decline")
}

func TestTailscaleForm_DeclineShowsCopyableSnippetWithoutWriting(t *testing.T) {
	m := NewTailscaleForm(newTestNamespace())
	m.tag = "tag:once"
	c, _ := m.Update(tailscaleConsentMsg{policyClientID: "k123", update: &docker.PolicyUpdate{}})
	m = c.(TailscaleForm)

	c, _ = m.Update(tailscaleDeclineMsg{})

	got := c.(TailscaleForm)
	assert.Equal(t, tailscaleFormStateSnippet, got.state)
	assert.Empty(t, got.policyClientID, "the one-shot policy client should be revoked and forgotten")

	view := got.View()
	assert.Contains(t, view, "tagOwners")
	assert.Contains(t, view, "tag:once")
	assert.Contains(t, view, "Continue")

	// The snippet view offers clipboard copy.
	_, cmd := got.Update(keyPressMsg("c"))
	assert.NotNil(t, cmd)
}

func TestTailscaleForm_PolicyStepFailureFallsBackToSnippet(t *testing.T) {
	m := NewTailscaleForm(newTestNamespace())
	m.running = true
	m.tag = "tag:once"

	c, _ := m.Update(tailscaleSnippetMsg{note: "Could not plan the policy update: boom"})

	got := c.(TailscaleForm)
	assert.Equal(t, tailscaleFormStateSnippet, got.state)
	view := got.View()
	assert.Contains(t, view, "boom")
	assert.Contains(t, view, "tagOwners")
}

func TestTailscaleForm_RevokeFailureIsSurfaced(t *testing.T) {
	m := NewTailscaleForm(newTestNamespace())
	m.tag = "tag:once"
	c, _ := m.Update(tailscaleDeclineMsg{})
	m = c.(TailscaleForm)

	c, _ = m.Update(tailscalePolicyRevokeFailedMsg{err: errors.New("HTTP 500")})

	got := c.(TailscaleForm)
	assert.Contains(t, got.View(), "Could not delete the one-shot policy OAuth client")
}

func TestTailscaleForm_FinishedErrorReturnsToSetup(t *testing.T) {
	m := NewTailscaleForm(newTestNamespace())
	m.running = true
	m.state = tailscaleFormStateSnippet

	c, _ := m.Update(tailscaleFormFinishedMsg{err: errors.New("probe failed")})

	got := c.(TailscaleForm)
	require.Error(t, got.err)
	assert.Equal(t, tailscaleFormStateSetup, got.state)
	assert.False(t, got.running)
	assert.Contains(t, got.View(), "probe failed")
}

func TestTailscaleForm_CloseRequestCleansUpPolicyClient(t *testing.T) {
	m := NewTailscaleForm(newTestNamespace())
	c, _ := m.Update(tailscaleConsentMsg{policyClientID: "k123", update: &docker.PolicyUpdate{}})
	m = c.(TailscaleForm)

	c, cmd := m.Update(tailscaleCloseRequestMsg{})

	got := c.(TailscaleForm)
	assert.Empty(t, got.policyClientID)
	assert.NotNil(t, cmd)
}
