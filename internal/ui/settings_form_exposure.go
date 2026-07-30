package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/once/internal/docker"
)

type SettingsFormExposure struct {
	settingsFormBase
}

func NewSettingsFormExposure(settings docker.ApplicationSettings, tailscaleEnabled bool) SettingsFormExposure {
	appName := docker.NameFromImageRef(settings.Image)

	radio := NewExposureRadioField()
	radio.SetSelected(exposureIndex(settings.Exposure))

	hostField := NewTextField("app.example.com")
	hostField.SetValue(settings.Host)
	radio.OnChange(func(selected int) {
		exposure := exposureAt(selected)
		if exposure == settings.Exposure {
			hostField.SetValue(settings.Host)
		} else {
			hostField.SetValue(exposureHostSuggestion(exposure, appName))
		}
	})

	m := SettingsFormExposure{
		settingsFormBase: settingsFormBase{
			title: "Exposure",
			form: NewForm("Save",
				FormItem{Key: "exposure", Label: "Exposure", Field: radio},
				FormItem{Key: "host", Label: "Host", Field: hostField, Required: true},
			),
		},
	}

	if !tailscaleEnabled {
		m.statusLine = func() string {
			return "Tailnet exposure requires Tailscale; enable it from the dashboard (press t) first."
		}
	}

	m.form.OnSubmit(func(f *Form) tea.Cmd {
		exposure := exposureAt(f.RadioField("exposure").Selected())
		if exposure == docker.ExposureTailnet && !tailscaleEnabled {
			f.SetError("exposure", "Tailscale is not enabled; enable it from the dashboard (press t) first")
			return nil
		}
		host, err := docker.ResolveHost(exposure, strings.TrimSpace(f.TextField("host").Value()), appName)
		if err != nil {
			f.SetError("host", docker.ErrorMessage(err))
			return nil
		}
		s := settings
		s.Exposure = exposure
		s.Host = host
		return func() tea.Msg { return SettingsSectionSubmitMsg{Settings: s} }
	})
	m.form.OnCancel(func(f *Form) tea.Cmd {
		return func() tea.Msg { return SettingsSectionCancelMsg{} }
	})

	return m
}

func (m SettingsFormExposure) Update(msg tea.Msg) (SettingsSection, tea.Cmd) {
	var cmd tea.Cmd
	m.settingsFormBase, cmd = m.update(msg)
	return m, cmd
}
