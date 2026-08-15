package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/once/internal/docker"
)

// SettingsFormBackups is the per-app side of backups: one participation
// checkbox. The Backup Repo itself (path, offsite, password) is install-wide
// and configured via `once backup enable`, not per app.
type SettingsFormBackups struct {
	settingsFormBase
}

func NewSettingsFormBackups(app *docker.Application, lastResult *docker.OperationResult) SettingsFormBackups {
	autoBackupField := NewCheckboxField("Include this app in backups", app.Settings.Backup.AutoBackup)

	m := SettingsFormBackups{
		settingsFormBase: settingsFormBase{
			title: "Backups",
			form: NewForm("Done",
				FormItem{Key: "auto-backup", Label: "Backups", Field: autoBackupField},
			),
		},
	}

	m.statusLine = func() string {
		return formatOperationStatus("backup", lastResult)
	}

	m.form.OnSubmit(func(f *Form) tea.Cmd {
		s := app.Settings
		s.Backup.AutoBackup = f.CheckboxField("auto-backup").Checked()
		return func() tea.Msg { return SettingsSectionSubmitMsg{Settings: s} }
	})
	m.form.OnCancel(func(f *Form) tea.Cmd {
		return func() tea.Msg { return SettingsSectionCancelMsg{} }
	})

	return m
}

func (m SettingsFormBackups) Update(msg tea.Msg) (SettingsSection, tea.Cmd) {
	var cmd tea.Cmd
	m.settingsFormBase, cmd = m.update(msg)
	return m, cmd
}
