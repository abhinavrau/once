package command

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/once/internal/mouse"
	"github.com/basecamp/once/internal/ui"
)

// runCustodyFlow hands the Repo Password over interactively: the shared
// custody component run as its own sequential bubbletea program, the same
// pattern runShowingProgress already uses (ADR-0013 amendment: reading a
// secret in stays TUI-only, handing one out reuses this shared component).
// Returns whether the user proved custody through the type-back gate.
func runCustodyFlow(kit ui.RecoveryKit) (bool, error) {
	m := &custodyProgram{flow: ui.NewCustody(kit)}
	if _, err := tea.NewProgram(m).Run(); err != nil {
		return false, err
	}
	return m.confirmed, nil
}

type custodyProgram struct {
	flow          ui.Custody
	confirmed     bool
	width, height int
}

func (m *custodyProgram) Init() tea.Cmd {
	return m.flow.Init()
}

func (m *custodyProgram) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case ui.CustodyClosedMsg:
		// The closed message carries the verdict as component state, so no
		// ordering between separate messages can lose a passed gate.
		m.confirmed = msg.Confirmed
		return m, tea.Quit

	case tea.KeyPressMsg:
		// ctrl+c abandons the flow: the password stays persisted, Enabled
		// stays false - the same outcome as the skip hatch.
		if key.Matches(msg, ui.NewKeyBinding("ctrl+c")) {
			return m, tea.Quit
		}

	case tea.MouseClickMsg:
		ms := msg.Mouse()
		var cmd tea.Cmd
		m.flow, cmd = m.flow.Update(ui.MouseEvent{
			X:       ms.X,
			Y:       ms.Y,
			Button:  ms.Button,
			Target:  mouse.Resolve(ms.X, ms.Y),
			IsClick: true,
		})
		return m, cmd
	}

	var cmd tea.Cmd
	m.flow, cmd = m.flow.Update(msg)
	return m, cmd
}

func (m *custodyProgram) View() tea.View {
	content := mouse.Sweep(lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.flow.View()))
	v := tea.NewView(content)
	v.AltScreen = true
	// Mouse tracking stays off on any screen showing the secret, so native
	// terminal selection can copy it; the gate keeps click-to-focus.
	if !m.flow.ShowingSecret() {
		v.MouseMode = tea.MouseModeAllMotion
	}
	return v
}
