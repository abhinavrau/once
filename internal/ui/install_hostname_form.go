package ui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/once/internal/docker"
)

type InstallHostnameBackMsg struct{}

type InstallHostnameForm struct {
	form     Form
	imageRef string
	title    string
}

func NewInstallHostnameForm(imageRef, title string, tailscaleEnabled bool) InstallHostnameForm {
	appName := docker.NameFromImageRef(imageRef)

	exposureField := NewExposureRadioField()
	if tailscaleEnabled {
		exposureField.SetSelected(exposureIndex(docker.ExposureTailnet))
	}

	hostField := NewTextField("app.example.com")
	hostField.SetValue(exposureHostSuggestion(exposureAt(exposureField.Selected()), appName))
	exposureField.OnChange(func(selected int) {
		hostField.SetValue(exposureHostSuggestion(exposureAt(selected), appName))
	})

	m := InstallHostnameForm{
		form: NewForm("Install",
			FormItem{Key: "exposure", Label: "Exposure", Field: exposureField},
			FormItem{Key: "host", Label: "Host", Field: hostField, Required: true},
		),
		imageRef: imageRef,
		title:    title,
	}

	m.form.OnSubmit(func(f *Form) tea.Cmd {
		return func() tea.Msg {
			return InstallFormSubmitMsg{
				ImageRef: imageRef,
				Hostname: f.TextField("host").Value(),
				Exposure: exposureAt(f.RadioField("exposure").Selected()),
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
