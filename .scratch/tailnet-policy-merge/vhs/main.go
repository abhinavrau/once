// Throwaway VHS harness: hosts the production TailscaleForm so the topology
// radio switch can be screen-recorded without a Docker daemon.
package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/once/internal/docker"
	"github.com/basecamp/once/internal/ui"
)

type app struct {
	form          ui.Component
	width, height int
}

func (a app) Init() tea.Cmd { return nil }

func (a app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" || msg.String() == "q" {
			return a, tea.Quit
		}
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
	}
	c, cmd := a.form.Update(msg)
	a.form = c
	return a, cmd
}

func (a app) View() tea.View {
	size := lipgloss.NewStyle().Faint(true).Render(fmt.Sprintf("%dx%d", a.width, a.height))
	content := lipgloss.JoinVertical(lipgloss.Left, a.form.View(), size)
	v := tea.NewView(lipgloss.Place(a.width, a.height, lipgloss.Center, lipgloss.Center, content))
	v.AltScreen = true
	return v
}

func main() {
	ns, err := docker.NewNamespace("vhs")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err := tea.NewProgram(app{form: ui.NewTailscaleForm(ns)}).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
