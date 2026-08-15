package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/once/internal/catalog"
)

type InstallImageSubmitMsg struct{ App catalog.App }
type InstallImageBackMsg struct{}

type InstallImageForm struct {
	form Form
}

func NewInstallImageForm() InstallImageForm {
	m := InstallImageForm{
		form: NewForm("Next", FormItem{
			Key:      "image",
			Label:    "Image",
			Field:    NewTextField("user/repo:tag"),
			Required: true,
		}),
	}

	m.form.OnSubmit(func(f *Form) tea.Cmd {
		entry := catalog.Resolve(f.TextField("image").Value())
		return func() tea.Msg { return InstallImageSubmitMsg{App: entry} }
	})
	m.form.OnCancel(func(f *Form) tea.Cmd {
		return func() tea.Msg { return InstallImageBackMsg{} }
	})

	return m
}

func (m InstallImageForm) Init() tea.Cmd {
	return m.form.Init()
}

func (m InstallImageForm) Update(msg tea.Msg) (InstallImageForm, tea.Cmd) {
	var cmd tea.Cmd
	m.form, cmd = m.form.Update(msg)
	return m, cmd
}

func (m InstallImageForm) View() string {
	return m.form.View()
}
