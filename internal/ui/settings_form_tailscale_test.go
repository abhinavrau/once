package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
)

func TestSettingsFormTailscale_InitialState(t *testing.T) {
	exposed := NewSettingsFormTailscale(docker.ApplicationSettings{Name: "app"}, true)
	assert.True(t, exposed.form.CheckboxField(tailscaleExposeField).Checked())

	hidden := NewSettingsFormTailscale(docker.ApplicationSettings{Name: "app", TailscaleExcluded: true}, true)
	assert.False(t, hidden.form.CheckboxField(tailscaleExposeField).Checked())
}

func TestSettingsFormTailscale_SubmitTogglesExposure(t *testing.T) {
	form := NewSettingsFormTailscale(docker.ApplicationSettings{Name: "app"}, true)
	form.form.CheckboxField(tailscaleExposeField).Toggle() // uncheck => excluded

	// Tab off the checkbox to the Done button, then submit.
	updateSettingsForm(&form, keyPressMsg("tab"))
	var section SettingsSection = form
	_, cmd := section.Update(keyPressMsg("enter"))
	require.NotNil(t, cmd)

	msg := cmd().(SettingsSectionSubmitMsg)
	assert.True(t, msg.Settings.TailscaleExcluded)
	assert.False(t, msg.Settings.TailscaleExposed())
}

func TestSettingsFormTailscale_StatusLineWhenDisabled(t *testing.T) {
	form := NewSettingsFormTailscale(docker.ApplicationSettings{Name: "app"}, false)
	assert.NotEmpty(t, form.StatusLine())
}
