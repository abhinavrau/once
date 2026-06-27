package ui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/once/internal/docker"
)

type InstallHostnameBackMsg struct{}

type InstallHostnameForm struct {
	form        Form
	imageRef    string
	title       string
	exposeField int // index of the Tailscale exposure checkbox, -1 when absent
}

func NewInstallHostnameForm(imageRef, title string, tailscaleEnabled bool) InstallHostnameForm {
	hostnameField := NewTextField("app.example.com")
	appName := docker.NameFromImageRef(imageRef)
	if appName != "" {
		hostnameField.SetPlaceholder(appName + ".example.com")
	}

	items := []FormItem{{Label: "Hostname", Field: hostnameField, Required: true}}
	exposeField := -1
	if tailscaleEnabled {
		exposeField = len(items)
		items = append(items, FormItem{Label: "Tailscale", Field: NewCheckboxField("Expose on the tailnet", true)})
	}

	m := InstallHostnameForm{
		form:        NewForm("Install", items...),
		imageRef:    imageRef,
		title:       title,
		exposeField: exposeField,
	}

	m.form.OnSubmit(func(f *Form) tea.Cmd {
		excluded := exposeField >= 0 && !f.CheckboxField(exposeField).Checked()
		return func() tea.Msg {
			return InstallFormSubmitMsg{
				ImageRef:          imageRef,
				Hostname:          f.TextField(0).Value(),
				TailscaleExcluded: excluded,
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
	return m.form.TextField(0).Value()
}
