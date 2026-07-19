package ui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/once/internal/catalog"
)

const installAppListOtherKey = -1

type (
	InstallAppSelectedMsg    struct{ ImageRef string }
	InstallCustomSelectedMsg struct{}
)

type InstallAppList struct {
	menu Menu
}

func NewInstallAppList() InstallAppList {
	items := make([]MenuItem, 0, len(catalog.Apps)+1)
	for i, app := range catalog.Apps {
		items = append(items, MenuItem{Label: app.Name, Key: i})
	}
	items = append(items, MenuItem{Label: "Custom Docker image", Key: installAppListOtherKey})

	return InstallAppList{menu: NewMenu(items...)}
}

func (m InstallAppList) Init() tea.Cmd {
	return nil
}

func (m InstallAppList) Update(msg tea.Msg) (InstallAppList, tea.Cmd) {
	switch msg := msg.(type) {
	case MenuSelectMsg:
		if msg.Key == installAppListOtherKey {
			return m, func() tea.Msg { return InstallCustomSelectedMsg{} }
		}
		app := catalog.Apps[msg.Key]
		return m, func() tea.Msg { return InstallAppSelectedMsg{ImageRef: app.ImageRef} }
	}

	var cmd tea.Cmd
	m.menu, cmd = m.menu.Update(msg)
	return m, cmd
}

func (m InstallAppList) View() string {
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Colors.Border).
		Padding(1, 4)

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(Colors.Primary).
		MarginBottom(1)

	title := titleStyle.Render("Choose an application")
	m.menu.SetWidth(lipgloss.Width(title))
	menuView := m.menu.View()

	content := lipgloss.JoinVertical(lipgloss.Left, title, menuView)

	return boxStyle.Render(content)
}
