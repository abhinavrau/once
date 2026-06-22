package ui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstallHostnameForm_Submit(t *testing.T) {
	form := NewInstallHostnameForm("ghcr.io/basecamp/once-campfire", "", false)

	hostnameFormTypeText(&form, "chat.example.com")
	hostnameFormPressTab(&form)
	form, cmd := form.Update(keyPressMsg("enter"))
	require.NotNil(t, cmd)

	msg := cmd()
	submit, ok := msg.(InstallFormSubmitMsg)
	require.True(t, ok, "expected InstallFormSubmitMsg, got %T", msg)
	assert.Equal(t, "ghcr.io/basecamp/once-campfire", submit.ImageRef)
	assert.Equal(t, "chat.example.com", submit.Hostname)
	assert.False(t, submit.TailscaleExcluded)
}

func TestInstallHostnameForm_TailscaleExposureToggle(t *testing.T) {
	form := NewInstallHostnameForm("ghcr.io/basecamp/once-campfire", "", true)
	hostnameFormTypeText(&form, "chat.example.com")

	// Tab from hostname to the exposure checkbox, uncheck it.
	hostnameFormPressTab(&form)
	form, _ = form.Update(keyPressMsg(" "))

	// Tab to the submit button and submit.
	hostnameFormPressTab(&form)
	form, cmd := form.Update(keyPressMsg("enter"))
	require.NotNil(t, cmd)

	submit := cmd().(InstallFormSubmitMsg)
	assert.True(t, submit.TailscaleExcluded)
}

func TestInstallHostnameForm_TailscaleExposedByDefault(t *testing.T) {
	form := NewInstallHostnameForm("ghcr.io/basecamp/once-campfire", "", true)
	hostnameFormTypeText(&form, "chat.example.com")
	hostnameFormPressTab(&form) // hostname -> checkbox
	hostnameFormPressTab(&form) // checkbox -> submit
	_, cmd := form.Update(keyPressMsg("enter"))
	require.NotNil(t, cmd)

	submit := cmd().(InstallFormSubmitMsg)
	assert.False(t, submit.TailscaleExcluded)
}

func TestInstallHostnameForm_Cancel(t *testing.T) {
	form := NewInstallHostnameForm("ghcr.io/basecamp/once-campfire:latest", "", false)

	// Tab to submit, tab to cancel
	hostnameFormPressTab(&form)
	hostnameFormPressTab(&form)
	form, cmd := form.Update(keyPressMsg("enter"))
	require.NotNil(t, cmd)

	msg := cmd()
	_, ok := msg.(InstallHostnameBackMsg)
	assert.True(t, ok, "expected InstallHostnameBackMsg, got %T", msg)
}

func TestInstallHostnameForm_RequiresHostname(t *testing.T) {
	form := NewInstallHostnameForm("ghcr.io/basecamp/once-campfire:latest", "", false)

	// Tab to submit button, then press enter with empty hostname
	hostnameFormPressTab(&form)
	form, _ = form.Update(keyPressMsg("enter"))
	assert.True(t, form.form.HasError())
}

func TestInstallHostnameForm_Hostname(t *testing.T) {
	form := NewInstallHostnameForm("ghcr.io/basecamp/once-campfire:latest", "", false)
	hostnameFormTypeText(&form, "app.example.com")
	assert.Equal(t, "app.example.com", form.Hostname())
}

func TestInstallHostnameForm_ShowsTitleWhenSet(t *testing.T) {
	form := NewInstallHostnameForm("ghcr.io/basecamp/once-campfire", "campfire", false)
	view := ansi.Strip(form.View())
	assert.Contains(t, view, "Installing campfire")
}

func TestInstallHostnameForm_NoTitleWhenEmpty(t *testing.T) {
	form := NewInstallHostnameForm("ghcr.io/basecamp/once-campfire", "", false)
	view := ansi.Strip(form.View())
	assert.NotContains(t, view, "Installing")
}

// Helpers

func hostnameFormTypeText(form *InstallHostnameForm, text string) {
	for _, r := range text {
		*form, _ = form.Update(keyPressMsg(string(r)))
	}
}

func hostnameFormPressTab(form *InstallHostnameForm) {
	*form, _ = form.Update(keyPressMsg("tab"))
}
