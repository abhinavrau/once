package ui

import (
	"context"
	"errors"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/once/internal/docker"
)

const (
	tailscaleClientIDField = iota
	tailscaleClientSecretField
	tailscaleTagField
)

const tailscaleFormTextWidth = 72

var (
	tailscaleFormCloseKey = WithHelp(NewKeyBinding("esc"), "esc", "close")
	tailscaleCopyKey      = WithHelp(NewKeyBinding("ctrl+p"), "ctrl+p", "copy policy snippet")
)

type TailscaleFormCloseMsg struct{}

type tailscaleEnableSubmitMsg struct {
	id, secret, tag string
}

type tailscaleDisableSubmitMsg struct{}

type tailscaleFormFinishedMsg struct {
	err      error
	warnings []string
}

// TailscaleForm is the dashboard-global overlay for enabling/disabling
// Tailscale. Enabling runs the same flow as the CLI: probe the pasted OAuth
// credential and boot the Docktail topology. The credential's tag must be
// defined in the tailnet policy first; ctrl+p copies the required snippet.
type TailscaleForm struct {
	namespace     *docker.Namespace
	enabled       bool
	form          Form
	help          Help
	width, height int
	running       bool
	progress      Progress
	err           error
	warnings      []string
}

func NewTailscaleForm(ns *docker.Namespace) TailscaleForm {
	enabled, _ := ns.Tailscale().Enabled(context.Background())

	h := NewHelp()

	m := TailscaleForm{
		namespace: ns,
		enabled:   enabled,
		help:      h,
		progress:  NewProgress(0, Colors.Border),
	}

	if enabled {
		m.help.SetBindings([]key.Binding{tailscaleFormCloseKey})
		m.form = NewForm("Disable")
		m.form.OnSubmit(func(f *Form) tea.Cmd {
			return func() tea.Msg { return tailscaleDisableSubmitMsg{} }
		})
	} else {
		m.help.SetBindings([]key.Binding{tailscaleCopyKey, tailscaleFormCloseKey})

		clientIDField := NewTextField("K123456CNTRL")

		clientSecretField := NewTextField("tskey-client-...")
		clientSecretField.SetEchoPassword()

		tagField := NewTextField("tag:once")
		tagField.SetValue("tag:once")

		m.form = NewForm("Enable",
			FormItem{Label: "Client ID", Field: clientIDField, Required: true},
			FormItem{Label: "Client Secret", Field: clientSecretField, Required: true},
			FormItem{Label: "Tag", Field: tagField, Required: true},
		)
		m.form.OnSubmit(func(f *Form) tea.Cmd {
			return func() tea.Msg {
				return tailscaleEnableSubmitMsg{
					id:     f.TextField(tailscaleClientIDField).Value(),
					secret: f.TextField(tailscaleClientSecretField).Value(),
					tag:    f.TextField(tailscaleTagField).Value(),
				}
			}
		})
	}
	m.form.OnCancel(requestTailscaleFormClose)

	return m
}

func (m TailscaleForm) Init() tea.Cmd {
	return m.form.Init()
}

func (m TailscaleForm) Update(msg tea.Msg) (Component, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.SetWidth(m.width)
		m.progress = m.progress.SetWidth(m.width)
		m.form, _ = m.form.Update(msg)
		return m, nil

	case tea.KeyPressMsg:
		if m.running {
			return m, nil
		}
		if key.Matches(msg, tailscaleFormCloseKey) {
			return m, func() tea.Msg { return TailscaleFormCloseMsg{} }
		}
		if !m.enabled && key.Matches(msg, tailscaleCopyKey) {
			return m, tea.SetClipboard(docker.PolicySnippet(m.form.TextField(tailscaleTagField).Value()))
		}
		m.err = nil
		m.warnings = nil

	case MouseEvent:
		if m.running {
			return m, nil
		}

	case tailscaleEnableSubmitMsg:
		return m.startRunning(m.runEnable(msg))

	case tailscaleDisableSubmitMsg:
		return m.startRunning(func() tea.Msg {
			return tailscaleFormFinishedMsg{err: m.namespace.DisableTailscale(context.Background())}
		})

	case tailscaleFormFinishedMsg:
		m.running = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		// Shadowing warnings keep the form open so the user actually sees them.
		if len(msg.warnings) > 0 {
			m.warnings = msg.warnings
			return m, nil
		}
		return m, func() tea.Msg { return TailscaleFormCloseMsg{} }

	case ProgressTickMsg:
		if m.running {
			var cmd tea.Cmd
			m.progress, cmd = m.progress.Update(msg)
			return m, cmd
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.form, cmd = m.form.Update(msg)
	return m, cmd
}

func (m TailscaleForm) View() string {
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Colors.Border).
		Padding(1, 4)

	title := lipgloss.NewStyle().Bold(true).Foreground(Colors.Primary).MarginBottom(1).Render("Tailscale")

	body := m.setupView()
	if m.running {
		body = m.progress.View()
	}

	helpLine := lipgloss.NewStyle().MarginTop(1).Align(lipgloss.Center).Render(m.help.View())

	return boxStyle.Render(lipgloss.JoinVertical(lipgloss.Center, title, body, helpLine))
}

// Private

// startRunning switches to the progress view and runs cmd in the background.
func (m TailscaleForm) startRunning(cmd tea.Cmd) (Component, tea.Cmd) {
	m.running = true
	m.err = nil
	m.warnings = nil
	m.progress = NewProgress(m.width, Colors.Border)
	return m, tea.Batch(m.progress.Init(), cmd)
}

// runEnable runs the enable lifecycle in the background: the credential probe
// enforces the scopes and the tailnet policy before anything boots. Under the
// headscale control seam there is no SaaS OAuth, so the credentials are unused.
func (m TailscaleForm) runEnable(msg tailscaleEnableSubmitMsg) tea.Cmd {
	ns := m.namespace
	return func() tea.Msg {
		settings := docker.TailscaleSettings{Tag: msg.tag}
		if !docker.TailscaleControlSeamActive() {
			settings.ClientID, settings.ClientSecret = msg.id, msg.secret
		}
		warnings, err := ns.EnableTailscale(context.Background(), settings)
		return tailscaleFormFinishedMsg{err: err, warnings: warnings}
	}
}

func (m TailscaleForm) setupView() string {
	var parts []string
	if m.err != nil {
		parts = append(parts, lipgloss.NewStyle().Foreground(Colors.Error).Width(tailscaleFormTextWidth).Render(docker.ErrorMessage(m.err)))
		if errors.Is(m.err, docker.ErrTagNotInPolicy) {
			parts = append(parts, lipgloss.NewStyle().Foreground(Colors.Muted).Render("ctrl+p copies the required policy snippet."))
		}
		parts = append(parts, "")
	} else if len(m.warnings) > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(Colors.Warning).Width(tailscaleFormTextWidth).Render(strings.Join(m.warnings, "\n")), "")
	}

	intro := "Tailscale is enabled."
	if !m.enabled {
		intro = "Merge the tailnet policy snippet (ctrl+p copies it) at\nlogin.tailscale.com/admin/acls, then create an OAuth client in the\nadmin console (Settings -> Trust credentials) with write access to\nAuth Keys, Devices Core, and Services, tagged with the tag below,\nand paste it here."
	}
	parts = append(parts, lipgloss.NewStyle().Foreground(Colors.Muted).Render(intro), "", m.form.View())

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// Helpers

func requestTailscaleFormClose(f *Form) tea.Cmd {
	return func() tea.Msg { return TailscaleFormCloseMsg{} }
}
