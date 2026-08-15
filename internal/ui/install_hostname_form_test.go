package ui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/catalog"
	"github.com/basecamp/once/internal/docker"
)

func TestInstallHostnameForm_DefaultsToLocalWithSuggestion(t *testing.T) {
	form := NewInstallHostnameForm(catalog.App{ImageRef: "ghcr.io/basecamp/once-campfire"}, "", false)

	assert.Equal(t, docker.ExposureLocal, form.Exposure())
	assert.Equal(t, "once-campfire.localhost", form.Hostname())
}

func TestInstallHostnameForm_PreselectsTailnetWhenTailscaleEnabled(t *testing.T) {
	form := NewInstallHostnameForm(catalog.App{ImageRef: "ghcr.io/basecamp/once-campfire"}, "", true)

	assert.Equal(t, docker.ExposureTailnet, form.Exposure())
	assert.Equal(t, "once-campfire", form.Hostname())
}

func TestInstallHostnameForm_RadioChangeUpdatesSuggestion(t *testing.T) {
	form := NewInstallHostnameForm(catalog.App{ImageRef: "ghcr.io/basecamp/once-campfire"}, "", false)

	// The radio has initial focus; right cycles local → tailnet → public → local.
	form, _ = form.Update(keyPressMsg("right"))
	assert.Equal(t, docker.ExposureTailnet, form.Exposure())
	assert.Equal(t, "once-campfire", form.Hostname())

	form, _ = form.Update(keyPressMsg("right"))
	assert.Equal(t, docker.ExposurePublic, form.Exposure())
	assert.Equal(t, "", form.Hostname(), "public has no derived default")

	form, _ = form.Update(keyPressMsg("right"))
	assert.Equal(t, docker.ExposureLocal, form.Exposure())
	assert.Equal(t, "once-campfire.localhost", form.Hostname())
}

func TestInstallHostnameForm_Submit(t *testing.T) {
	form := NewInstallHostnameForm(catalog.App{ImageRef: "ghcr.io/basecamp/once-campfire"}, "", false)

	// Public empties the host; type an explicit FQDN and submit.
	form, _ = form.Update(keyPressMsg("right"))
	form, _ = form.Update(keyPressMsg("right"))
	hostnameFormPressTab(&form)
	hostnameFormTypeText(&form, "chat.example.com")
	hostnameFormPressTab(&form)
	form, cmd := form.Update(keyPressMsg("enter"))
	require.NotNil(t, cmd)

	msg := cmd()
	submit, ok := msg.(InstallFormSubmitMsg)
	require.True(t, ok, "expected InstallFormSubmitMsg, got %T", msg)
	assert.Equal(t, "ghcr.io/basecamp/once-campfire", submit.App.ImageRef)
	assert.Equal(t, "chat.example.com", submit.Hostname)
	assert.Equal(t, docker.ExposurePublic, submit.Exposure)
}

func TestInstallHostnameForm_SubmitAcceptsSuggestion(t *testing.T) {
	form := NewInstallHostnameForm(catalog.App{ImageRef: "ghcr.io/basecamp/once-campfire"}, "", false)

	hostnameFormPressTab(&form)
	hostnameFormPressTab(&form)
	form, cmd := form.Update(keyPressMsg("enter"))
	require.NotNil(t, cmd)

	submit, ok := cmd().(InstallFormSubmitMsg)
	require.True(t, ok)
	assert.Equal(t, "once-campfire.localhost", submit.Hostname)
	assert.Equal(t, docker.ExposureLocal, submit.Exposure)
}

func TestInstallHostnameForm_Cancel(t *testing.T) {
	form := NewInstallHostnameForm(catalog.App{ImageRef: "ghcr.io/basecamp/once-campfire:latest"}, "", false)

	// Tab past the host field and submit button to cancel
	hostnameFormPressTab(&form)
	hostnameFormPressTab(&form)
	hostnameFormPressTab(&form)
	form, cmd := form.Update(keyPressMsg("enter"))
	require.NotNil(t, cmd)

	msg := cmd()
	_, ok := msg.(InstallHostnameBackMsg)
	assert.True(t, ok, "expected InstallHostnameBackMsg, got %T", msg)
}

func TestInstallHostnameForm_PublicRequiresHostname(t *testing.T) {
	form := NewInstallHostnameForm(catalog.App{ImageRef: "ghcr.io/basecamp/once-campfire:latest"}, "", false)

	form, _ = form.Update(keyPressMsg("right"))
	form, _ = form.Update(keyPressMsg("right"))
	require.Equal(t, docker.ExposurePublic, form.Exposure())

	// Tab to submit button, then press enter with the empty host
	hostnameFormPressTab(&form)
	hostnameFormPressTab(&form)
	form, _ = form.Update(keyPressMsg("enter"))
	assert.True(t, form.form.HasError())
}

func TestInstallHostnameForm_ShowsTitleWhenSet(t *testing.T) {
	form := NewInstallHostnameForm(catalog.App{ImageRef: "ghcr.io/basecamp/once-campfire"}, "campfire", false)
	view := ansi.Strip(form.View())
	assert.Contains(t, view, "Installing campfire")
}

func TestInstallHostnameForm_NoTitleWhenEmpty(t *testing.T) {
	form := NewInstallHostnameForm(catalog.App{ImageRef: "ghcr.io/basecamp/once-campfire"}, "", false)
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
