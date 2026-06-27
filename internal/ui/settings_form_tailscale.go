package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/once/internal/docker"
)

const tailscaleExposeField = 0

type SettingsFormTailscale struct {
	settingsFormBase
}

func NewSettingsFormTailscale(settings docker.ApplicationSettings, tailscaleEnabled bool) SettingsFormTailscale {
	exposeField := NewCheckboxField("Expose on the tailnet", settings.TailscaleExposed())

	m := SettingsFormTailscale{
		settingsFormBase: settingsFormBase{
			title: "Tailscale",
			form: NewForm("Done",
				FormItem{Label: "Tailscale", Field: exposeField},
			),
		},
	}

	if !tailscaleEnabled {
		m.statusLine = func() string {
			return "Tailscale is not enabled globally; this preference applies once it is."
		}
	}

	m.form.OnSubmit(func(f *Form) tea.Cmd {
		s := settings
		s.TailscaleExcluded = !f.CheckboxField(tailscaleExposeField).Checked()
		return func() tea.Msg { return SettingsSectionSubmitMsg{Settings: s} }
	})
	m.form.OnCancel(func(f *Form) tea.Cmd {
		return func() tea.Msg { return SettingsSectionCancelMsg{} }
	})

	return m
}

func (m SettingsFormTailscale) Update(msg tea.Msg) (SettingsSection, tea.Cmd) {
	var cmd tea.Cmd
	m.settingsFormBase, cmd = m.update(msg)
	return m, cmd
}
