package ui

import (
	"context"
	"errors"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/once/internal/docker"
)

const (
	tailscaleEnableField = iota
	tailscaleClientIDField
	tailscaleClientSecretField
	tailscaleTagField
)

var tailscaleFormCloseKey = WithHelp(NewKeyBinding("esc"), "esc", "close")

type TailscaleFormCloseMsg struct{}

type tailscaleFormSubmitMsg struct {
	enable bool
	id     string
	secret string
	tag    string
}

type tailscaleFormFinishedMsg struct {
	err error
}

// TailscaleForm is the dashboard-global overlay for enabling/disabling
// Tailscale. It's a thin front-end over the same Namespace lifecycle the CLI
// uses (EnableTailscale/DisableTailscale).
type TailscaleForm struct {
	namespace     *docker.Namespace
	form          Form
	help          Help
	width, height int
	running       bool
	progress      Progress
	err           error
}

func NewTailscaleForm(ns *docker.Namespace) TailscaleForm {
	enabled, _ := ns.Tailscale().Enabled(context.Background())
	var current docker.TailscaleSettings
	if enabled {
		current, _ = ns.Tailscale().LoadSettings(context.Background())
	}

	enableField := NewCheckboxField("Enabled", enabled)

	clientIDField := NewTextField("OAuth client ID")
	clientIDField.SetValue(current.ClientID)

	clientSecretField := NewTextField("OAuth client secret")
	clientSecretField.SetValue(current.ClientSecret)
	clientSecretField.SetEchoPassword()

	tagField := NewTextField("Tag the OAuth client owns, e.g. tag:once")
	tagField.SetValue(current.Tag)

	h := NewHelp()
	h.SetBindings([]key.Binding{tailscaleFormCloseKey})

	m := TailscaleForm{
		namespace: ns,
		form: NewForm("Save",
			FormItem{Label: "Enable Tailscale", Field: enableField},
			FormItem{Label: "OAuth Client ID", Field: clientIDField},
			FormItem{Label: "OAuth Client Secret", Field: clientSecretField},
			FormItem{Label: "Tag", Field: tagField},
		),
		help:     h,
		progress: NewProgress(0, Colors.Border),
	}

	m.form.OnSubmit(func(f *Form) tea.Cmd {
		return func() tea.Msg {
			return tailscaleFormSubmitMsg{
				enable: f.CheckboxField(tailscaleEnableField).Checked(),
				id:     f.TextField(tailscaleClientIDField).Value(),
				secret: f.TextField(tailscaleClientSecretField).Value(),
				tag:    f.TextField(tailscaleTagField).Value(),
			}
		}
	})
	m.form.OnCancel(func(f *Form) tea.Cmd {
		return func() tea.Msg { return TailscaleFormCloseMsg{} }
	})

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
		m.err = nil

	case MouseEvent:
		if m.running {
			return m, nil
		}

	case tailscaleFormSubmitMsg:
		if msg.enable && (msg.id == "" || msg.secret == "" || msg.tag == "") {
			m.err = errors.New("OAuth Client ID, Secret, and Tag are required")
			return m, nil
		}
		m.running = true
		m.err = nil
		m.progress = NewProgress(m.width, Colors.Border)
		return m, tea.Batch(m.progress.Init(), m.run(msg))

	case tailscaleFormFinishedMsg:
		m.running = false
		if msg.err != nil {
			m.err = msg.err
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

	var body string
	if m.running {
		body = m.progress.View()
	} else {
		var status string
		if m.err != nil {
			status = lipgloss.NewStyle().Foreground(Colors.Error).Render(docker.ErrorMessage(m.err))
		}
		body = lipgloss.JoinVertical(lipgloss.Left, status, "", m.form.View())
	}

	helpLine := lipgloss.NewStyle().MarginTop(1).Align(lipgloss.Center).Render(m.help.View())

	return boxStyle.Render(lipgloss.JoinVertical(lipgloss.Center, title, body, helpLine))
}

// Private

func (m TailscaleForm) run(msg tailscaleFormSubmitMsg) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		var err error
		if msg.enable {
			err = m.namespace.EnableTailscale(ctx, docker.TailscaleSettings{ClientID: msg.id, ClientSecret: msg.secret, Tag: msg.tag})
		} else {
			err = m.namespace.DisableTailscale(ctx)
		}
		return tailscaleFormFinishedMsg{err: err}
	}
}
