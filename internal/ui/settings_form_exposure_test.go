package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
)

func TestSettingsFormExposure_InitialState(t *testing.T) {
	form := newExposureForm(t, docker.ExposureLocal, "chat.localhost", true)

	assert.Equal(t, exposureIndex(docker.ExposureLocal), form.form.RadioField("exposure").Selected())
	assert.Equal(t, "chat.localhost", form.form.TextField("host").Value())
}

func TestSettingsFormExposure_SwitchSuggestsDerivedHost(t *testing.T) {
	form := newExposureForm(t, docker.ExposureLocal, "chat.localhost", true)

	// The radio has initial focus; right moves local → tailnet.
	exposurePressKey(&form, "right")
	assert.Equal(t, "once-campfire", form.form.TextField("host").Value())

	exposurePressKey(&form, "right")
	assert.Equal(t, "", form.form.TextField("host").Value(), "public has no derived default")

	// Cycling back to the app's stored exposure restores its stored host.
	exposurePressKey(&form, "right")
	assert.Equal(t, "chat.localhost", form.form.TextField("host").Value())
}

func TestSettingsFormExposure_SubmitSwitchesExposure(t *testing.T) {
	form := newExposureForm(t, docker.ExposureLocal, "chat.localhost", true)

	exposurePressKey(&form, "right") // → tailnet
	exposurePressKey(&form, "tab")   // → host
	exposurePressKey(&form, "tab")   // → save
	msg := exposureSubmit(t, &form)

	submit, ok := msg.(SettingsSectionSubmitMsg)
	require.True(t, ok, "expected SettingsSectionSubmitMsg, got %T", msg)
	assert.Equal(t, docker.ExposureTailnet, submit.Settings.Exposure)
	assert.Equal(t, "once-campfire", submit.Settings.Host)
}

func TestSettingsFormExposure_SubmitResolvesBareLocalHost(t *testing.T) {
	form := newExposureForm(t, docker.ExposureLocal, "chat.localhost", true)

	form.form.TextField("host").SetValue("blog")
	exposurePressKey(&form, "tab")
	exposurePressKey(&form, "tab")
	msg := exposureSubmit(t, &form)

	submit, ok := msg.(SettingsSectionSubmitMsg)
	require.True(t, ok)
	assert.Equal(t, "blog.localhost", submit.Settings.Host)
}

func TestSettingsFormExposure_PublicRequiresFQDN(t *testing.T) {
	form := newExposureForm(t, docker.ExposureLocal, "chat.localhost", true)

	exposurePressKey(&form, "right") // → tailnet
	exposurePressKey(&form, "right") // → public, host emptied
	form.form.TextField("host").SetValue("chat")
	exposurePressKey(&form, "tab")
	exposurePressKey(&form, "tab")

	result, cmd := form.Update(keyPressMsg("enter"))
	form = result.(SettingsFormExposure)
	assert.True(t, form.form.HasError())

	rejected, ok := findFormRejection(cmd)
	require.True(t, ok, "expected a FormSubmitRejectedMsg")
	assert.Equal(t, "host", rejected.Key)
	assert.Equal(t, form.form.Error(), rejected.Reason)
}

func TestSettingsFormExposure_TailnetBlockedWithoutTailscale(t *testing.T) {
	form := newExposureForm(t, docker.ExposureLocal, "chat.localhost", false)

	assert.NotEmpty(t, form.StatusLine())

	exposurePressKey(&form, "right") // → tailnet
	exposurePressKey(&form, "tab")
	exposurePressKey(&form, "tab")

	result, cmd := form.Update(keyPressMsg("enter"))
	form = result.(SettingsFormExposure)
	assert.True(t, form.form.HasError())
	assert.Contains(t, form.form.Error(), "Tailscale")
	assert.Equal(t, 0, form.form.Focused(), "refocuses the exposure field")

	rejected, ok := findFormRejection(cmd)
	require.True(t, ok, "expected a FormSubmitRejectedMsg")
	assert.Equal(t, "exposure", rejected.Key)
	assert.Equal(t, form.form.Error(), rejected.Reason)

	exposurePressKey(&form, "left")
	assert.False(t, form.form.HasError(), "typing in the refocused field clears the inline error")
}

func TestSettingsFormExposure_Cancel(t *testing.T) {
	form := newExposureForm(t, docker.ExposureLocal, "chat.localhost", true)

	for range 3 {
		exposurePressKey(&form, "tab")
	}
	msg := exposureSubmit(t, &form)

	_, ok := msg.(SettingsSectionCancelMsg)
	assert.True(t, ok, "expected SettingsSectionCancelMsg, got %T", msg)
}

// Helpers

func newExposureForm(t *testing.T, exposure docker.Exposure, host string, tailscaleEnabled bool) SettingsFormExposure {
	t.Helper()
	return NewSettingsFormExposure(docker.ApplicationSettings{
		Name:     "myapp",
		Image:    "ghcr.io/basecamp/once-campfire:latest",
		Host:     host,
		Exposure: exposure,
	}, tailscaleEnabled)
}

func exposurePressKey(form *SettingsFormExposure, key string) {
	updateSettingsForm(form, keyPressMsg(key))
}

func exposureSubmit(t *testing.T, form *SettingsFormExposure) tea.Msg {
	t.Helper()
	result, cmd := form.Update(keyPressMsg("enter"))
	*form = result.(SettingsFormExposure)
	require.NotNil(t, cmd)
	return cmd()
}
