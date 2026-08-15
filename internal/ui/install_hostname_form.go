package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/once/internal/catalog"
	"github.com/basecamp/once/internal/docker"
)

type InstallHostnameBackMsg struct{}

// InstallBackupToggledMsg reports the participation checkbox being ticked, so
// the install screen can offer the install-wide Backups overlay inline when
// no Backup Repo is configured yet.
type InstallBackupToggledMsg struct{}

type InstallHostnameForm struct {
	form        Form
	app         catalog.App
	title       string
	customImage bool
}

// NewInstallHostnameForm builds the install form: exposure and host for every
// app, plus the backup participation checkbox. A custom image also asks the
// data path and - unconditionally, backups or not - the health check path:
// verifyHTTP's destructive default must never apply to an app that answers
// elsewhere. Catalog apps carry both in their Profile already.
func NewInstallHostnameForm(app catalog.App, title string, tailscaleEnabled, customImage bool) InstallHostnameForm {
	appName := app.Profile.AppName(app.ImageRef)

	exposureField := NewExposureRadioField()
	if tailscaleEnabled {
		exposureField.SetSelected(exposureIndex(docker.ExposureTailnet))
	}

	hostField := NewTextField("app.example.com")
	hostField.SetValue(exposureHostSuggestion(exposureAt(exposureField.Selected()), appName))
	exposureField.OnChange(func(selected int) {
		hostField.SetValue(exposureHostSuggestion(exposureAt(selected), appName))
	})

	autoBackupField := NewCheckboxField("Include this app in backups", false)
	autoBackupField.OnToggle(func(checked bool) tea.Cmd {
		if !checked {
			return nil
		}
		return func() tea.Msg { return InstallBackupToggledMsg{} }
	})

	items := []FormItem{
		{Key: "exposure", Label: "Exposure", Field: exposureField},
		{Key: "host", Label: "Host", Field: hostField, Required: true},
	}
	if customImage {
		dataPathField := NewTextField("/data")
		healthPathField := NewTextField(docker.DefaultHealthCheckPath)
		items = append(items,
			FormItem{Key: "data-path", Label: "Data path", Field: dataPathField},
			FormItem{Key: "health-path", Label: "Health check path", Field: healthPathField},
		)
	}
	items = append(items, FormItem{Key: "auto-backup", Label: "Backups", Field: autoBackupField})

	m := InstallHostnameForm{
		form:        NewForm("Install", items...),
		app:         app,
		title:       title,
		customImage: customImage,
	}

	m.form.OnSubmit(func(f *Form) tea.Cmd {
		submitted := app
		autoBackup := f.CheckboxField("auto-backup").Checked()
		if customImage {
			submitted.Profile.DataPath = strings.TrimSpace(f.TextField("data-path").Value())
			submitted.Profile.HealthPath = strings.TrimSpace(f.TextField("health-path").Value())
			// A wrong data path archives an empty directory, which is worse
			// than no backup because it looks like one.
			if autoBackup && submitted.Profile.DataPath == "" {
				f.SetError("data-path", "A data path is required to back up a custom image")
				return nil
			}
		}
		return func() tea.Msg {
			return InstallFormSubmitMsg{
				App:        submitted,
				Hostname:   f.TextField("host").Value(),
				Exposure:   exposureAt(f.RadioField("exposure").Selected()),
				AutoBackup: autoBackup,
			}
		}
	})
	m.form.OnCancel(func(f *Form) tea.Cmd {
		return func() tea.Msg { return InstallHostnameBackMsg{} }
	})

	return m
}

func (m InstallHostnameForm) Init() tea.Cmd {
	return m.form.Init()
}

func (m InstallHostnameForm) Update(msg tea.Msg) (InstallHostnameForm, tea.Cmd) {
	var cmd tea.Cmd
	m.form, cmd = m.form.Update(msg)
	return m, cmd
}

func (m InstallHostnameForm) View() string {
	if m.title != "" {
		titleLine := Styles.Title.Render("Installing " + m.title)
		return lipgloss.JoinVertical(lipgloss.Center, titleLine, "", m.form.View())
	}
	return m.form.View()
}

func (m InstallHostnameForm) Hostname() string {
	return m.form.TextField("host").Value()
}

func (m InstallHostnameForm) Exposure() docker.Exposure {
	return exposureAt(m.form.RadioField("exposure").Selected())
}
