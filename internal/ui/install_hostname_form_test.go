package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/catalog"
	"github.com/basecamp/once/internal/docker"
)

func TestInstallHostnameForm_DefaultsToLocalWithSuggestion(t *testing.T) {
	form := NewInstallHostnameForm(catalog.App{ImageRef: "ghcr.io/basecamp/once-campfire"}, "", false, false)

	assert.Equal(t, docker.ExposureLocal, form.Exposure())
	assert.Equal(t, "once-campfire.localhost", form.Hostname())
}

func TestInstallHostnameForm_PreselectsTailnetWhenTailscaleEnabled(t *testing.T) {
	form := NewInstallHostnameForm(catalog.App{ImageRef: "ghcr.io/basecamp/once-campfire"}, "", true, false)

	assert.Equal(t, docker.ExposureTailnet, form.Exposure())
	assert.Equal(t, "once-campfire", form.Hostname())
}

func TestInstallHostnameForm_RadioChangeUpdatesSuggestion(t *testing.T) {
	form := NewInstallHostnameForm(catalog.App{ImageRef: "ghcr.io/basecamp/once-campfire"}, "", false, false)

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
	form := NewInstallHostnameForm(catalog.App{ImageRef: "ghcr.io/basecamp/once-campfire"}, "", false, false)

	// Public empties the host; type an explicit FQDN and submit.
	form, _ = form.Update(keyPressMsg("right"))
	form, _ = form.Update(keyPressMsg("right"))
	hostnameFormPressTab(&form)
	hostnameFormTypeText(&form, "chat.example.com")
	form, cmd := form.Update(MouseEvent{Target: "submit", IsClick: true})
	require.NotNil(t, cmd)

	msg := cmd()
	submit, ok := msg.(InstallFormSubmitMsg)
	require.True(t, ok, "expected InstallFormSubmitMsg, got %T", msg)
	assert.Equal(t, "ghcr.io/basecamp/once-campfire", submit.App.ImageRef)
	assert.Equal(t, "chat.example.com", submit.Hostname)
	assert.Equal(t, docker.ExposurePublic, submit.Exposure)
	assert.False(t, submit.AutoBackup, "participation defaults to unticked")
}

func TestInstallHostnameForm_SubmitAcceptsSuggestion(t *testing.T) {
	form := NewInstallHostnameForm(catalog.App{ImageRef: "ghcr.io/basecamp/once-campfire"}, "", false, false)

	_, cmd := form.Update(MouseEvent{Target: "submit", IsClick: true})
	require.NotNil(t, cmd)

	submit, ok := cmd().(InstallFormSubmitMsg)
	require.True(t, ok)
	assert.Equal(t, "once-campfire.localhost", submit.Hostname)
	assert.Equal(t, docker.ExposureLocal, submit.Exposure)
}

func TestInstallHostnameForm_Cancel(t *testing.T) {
	form := NewInstallHostnameForm(catalog.App{ImageRef: "ghcr.io/basecamp/once-campfire:latest"}, "", false, false)

	_, cmd := form.Update(MouseEvent{Target: "cancel", IsClick: true})
	require.NotNil(t, cmd)

	msg := cmd()
	_, ok := msg.(InstallHostnameBackMsg)
	assert.True(t, ok, "expected InstallHostnameBackMsg, got %T", msg)
}

func TestInstallHostnameForm_PublicRequiresHostname(t *testing.T) {
	form := NewInstallHostnameForm(catalog.App{ImageRef: "ghcr.io/basecamp/once-campfire:latest"}, "", false, false)

	form, _ = form.Update(keyPressMsg("right"))
	form, _ = form.Update(keyPressMsg("right"))
	require.Equal(t, docker.ExposurePublic, form.Exposure())

	form, _ = form.Update(MouseEvent{Target: "submit", IsClick: true})
	assert.True(t, form.form.HasError())
}

// A catalog app carries its profile already: no data path or health path
// fields, just the participation checkbox.
func TestInstallHostnameForm_CatalogAsksOnlyCheckbox(t *testing.T) {
	form := NewInstallHostnameForm(catalog.Resolve("vaultwarden"), "", false, false)

	view := ansi.Strip(form.View())
	assert.Contains(t, view, "Include this app in backups")
	assert.NotContains(t, view, "Data path")
	assert.NotContains(t, view, "Health check path")
}

// A custom image is asked its data path and - unconditionally, backups or
// not - the health check path, both riding the submitted App's Profile.
func TestInstallHostnameForm_CustomImageAsksProfileFields(t *testing.T) {
	form := NewInstallHostnameForm(catalog.App{ImageRef: "vaultwarden/server"}, "", false, true)

	view := ansi.Strip(form.View())
	assert.Contains(t, view, "Data path")
	assert.Contains(t, view, "Health check path")

	form, _ = form.Update(MouseEvent{Target: "field:data-path", IsClick: true})
	hostnameFormTypeText(&form, "/data")
	form, _ = form.Update(MouseEvent{Target: "field:health-path", IsClick: true})
	hostnameFormTypeText(&form, "/alive")
	form, cmd := form.Update(MouseEvent{Target: "submit", IsClick: true})
	require.NotNil(t, cmd)

	submit, ok := cmd().(InstallFormSubmitMsg)
	require.True(t, ok)
	assert.Equal(t, "/data", submit.App.Profile.DataPath)
	assert.Equal(t, "/alive", submit.App.Profile.HealthPath)
}

// Ticking the checkbox forces a data path for a custom image: a wrong data
// path archives an empty directory, which looks like a backup and is not.
func TestInstallHostnameForm_BackupRequiresDataPathForCustomImage(t *testing.T) {
	form := NewInstallHostnameForm(catalog.App{ImageRef: "vaultwarden/server"}, "", false, true)

	form, _ = form.Update(MouseEvent{Target: "field:auto-backup", IsClick: true})
	form, cmd := form.Update(MouseEvent{Target: "submit", IsClick: true})
	require.True(t, form.form.HasError())
	assert.Contains(t, form.form.Error(), "data path")

	// The rejection cmd carries no submit message.
	for _, msg := range collectMsgs(cmd) {
		_, ok := msg.(InstallFormSubmitMsg)
		assert.False(t, ok, "a rejected submit must not emit InstallFormSubmitMsg")
	}
}

// Ticking the participation checkbox announces itself, so the install screen
// can offer the Backups overlay inline when no repo is configured yet.
func TestInstallHostnameForm_TickEmitsBackupToggledMsg(t *testing.T) {
	form := NewInstallHostnameForm(catalog.App{ImageRef: "ghcr.io/basecamp/once-campfire"}, "", false, false)

	form, cmd := form.Update(MouseEvent{Target: "field:auto-backup", IsClick: true})
	assert.Contains(t, collectMsgs(cmd), tea.Msg(InstallBackupToggledMsg{}))

	// Unticking stays silent.
	_, cmd = form.Update(MouseEvent{Target: "field:auto-backup", IsClick: true})
	assert.NotContains(t, collectMsgs(cmd), tea.Msg(InstallBackupToggledMsg{}))
}

func TestInstallHostnameForm_ShowsTitleWhenSet(t *testing.T) {
	form := NewInstallHostnameForm(catalog.App{ImageRef: "ghcr.io/basecamp/once-campfire"}, "campfire", false, false)
	view := ansi.Strip(form.View())
	assert.Contains(t, view, "Installing campfire")
}

func TestInstallHostnameForm_NoTitleWhenEmpty(t *testing.T) {
	form := NewInstallHostnameForm(catalog.App{ImageRef: "ghcr.io/basecamp/once-campfire"}, "", false, false)
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
