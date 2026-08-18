package ui

import (
	"context"
	"errors"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/once/internal/docker"
)

// tailscaleModeChoices maps the mode radio's option order to settings values;
// sidecar first so a fresh enable defaults to it (ADR-0007).
var tailscaleModeChoices = []string{docker.TailscaleModeSidecar, docker.TailscaleModeHost}

const tailscaleFormTextWidth = 72

var (
	tailscaleFormCloseKey = WithHelp(NewKeyBinding("esc"), "esc", "close")
	tailscaleCopyKey      = WithHelp(NewKeyBinding("ctrl+p"), "ctrl+p", "copy policy snippet")
)

type TailscaleFormCloseMsg struct{}

type tailscaleEnableSubmitMsg struct {
	id, secret, tag, mode string
}

type tailscaleDisableSubmitMsg struct{}

type tailscaleFormFinishedMsg struct {
	err      error
	warnings []string
}

type tailscaleStatusMsg string

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
	status        string
	statusChan    chan string
	err           error
	warnings      []string
}

func NewTailscaleForm(ns *docker.Namespace) TailscaleForm {
	enabled, _ := ns.Tailscale().Enabled(context.Background())
	var settings docker.TailscaleSettings
	if enabled {
		settings, _ = ns.Tailscale().LoadSettings(context.Background())
	}
	return newTailscaleForm(ns, enabled, settings)
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
		if key.Matches(msg, tailscaleCopyKey) {
			return m, tea.SetClipboard(docker.PolicySnippet(m.form.TextField("tag").Value()))
		}
		m.err = nil
		m.warnings = nil

	case MouseEvent:
		if m.running {
			return m, nil
		}

	case tailscaleEnableSubmitMsg:
		m.statusChan = make(chan string, 4)
		c, cmd := m.startRunning(m.runEnable(msg))
		return c, tea.Batch(cmd, listenTailscaleStatus(m.statusChan))

	case tailscaleDisableSubmitMsg:
		return m.startRunning(func() tea.Msg {
			return tailscaleFormFinishedMsg{err: m.namespace.DisableTailscale(context.Background())}
		})

	case tailscaleStatusMsg:
		if !m.running || m.statusChan == nil {
			return m, nil
		}
		m.status = string(msg)
		return m, listenTailscaleStatus(m.statusChan)

	case tailscaleFormFinishedMsg:
		m.running = false
		m.statusChan = nil
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
		if m.status != "" {
			status := lipgloss.NewStyle().Foreground(Colors.Muted).Width(tailscaleFormTextWidth).Render(m.status)
			body = lipgloss.JoinVertical(lipgloss.Center, status, "", body)
		}
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
	m.status = ""
	m.progress = NewProgress(m.width, Colors.Border)
	return m, tea.Batch(m.progress.Init(), cmd)
}

// runEnable runs the enable lifecycle in the background, forwarding its waiting
// stages to the status channel. The form passes on exactly what was collected -
// validating and normalizing the settings is the lifecycle's job.
func (m TailscaleForm) runEnable(msg tailscaleEnableSubmitMsg) tea.Cmd {
	ns, statuses := m.namespace, m.statusChan
	return func() tea.Msg {
		settings := docker.TailscaleSettings{
			ClientID:     msg.id,
			ClientSecret: msg.secret,
			Tag:          msg.tag,
			Mode:         msg.mode,
			// No topology selector yet (ticket 05): pin the TUI to Services
			// so its behavior is unchanged until then.
			Topology: docker.TailnetTopologyServices,
		}
		progress := func(p docker.DeployProgress) {
			if p.Stage == docker.DeployStageWaiting {
				statuses <- p.Status
			}
		}
		warnings, err := ns.EnableTailscale(context.Background(), settings, progress)
		close(statuses)
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

	intro := "Tailscale is enabled. Enable re-applies the settings below\n(changing the mode, credentials, or tag rebuilds the topology)."
	if !m.enabled {
		intro = "Merge the tailnet policy snippet (ctrl+p copies it) at\nlogin.tailscale.com/admin/acls, then create an OAuth client in the\nadmin console (Settings -> Trust credentials) with write access to\nAuth Keys, Devices Core, and Services, tagged with the tag below,\nand paste it here."
	}
	parts = append(parts, lipgloss.NewStyle().Foreground(Colors.Muted).Render(intro), "", m.form.View())

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// Helpers

// newTailscaleForm builds the overlay for a known enabled state. On an enabled
// install the fields show the stored settings and submitting re-enables (the
// same lifecycle the CLI runs: unchanged settings are a no-op, changed ones
// rebuild the topology), with Disable as a secondary action. The fields are not
// required there because an enabled install's stored credentials may be blank;
// requiring them would leave neither action able to submit.
func newTailscaleForm(ns *docker.Namespace, enabled bool, settings docker.TailscaleSettings) TailscaleForm {
	m := TailscaleForm{
		namespace: ns,
		enabled:   enabled,
		help:      NewHelp(),
		progress:  NewProgress(0, Colors.Border),
	}
	m.help.SetBindings([]key.Binding{tailscaleCopyKey, tailscaleFormCloseKey})

	clientIDField := NewTextField("K123456CNTRL")
	clientIDField.SetValue(settings.ClientID)

	clientSecretField := NewTextField("tskey-client-...")
	clientSecretField.SetEchoPassword()
	clientSecretField.SetValue(settings.ClientSecret)

	tagField := NewTextField("tag:once")
	if enabled {
		tagField.SetValue(settings.Tag)
	} else {
		tagField.SetValue("tag:once")
	}

	modeField := NewRadioField("Sidecar", "Host")
	if i := slices.Index(tailscaleModeChoices, settings.Mode); i >= 0 {
		modeField.SetSelected(i)
	}

	m.form = NewForm("Enable",
		FormItem{Key: "client-id", Label: "Client ID", Field: clientIDField, Required: !enabled},
		FormItem{Key: "client-secret", Label: "Client Secret", Field: clientSecretField, Required: !enabled},
		FormItem{Key: "tag", Label: "Tag", Field: tagField, Required: !enabled},
		FormItem{Key: "mode", Label: "Mode", Field: modeField},
	)
	m.form.OnSubmit(func(f *Form) tea.Cmd {
		return func() tea.Msg {
			return tailscaleEnableSubmitMsg{
				id:     f.TextField("client-id").Value(),
				secret: f.TextField("client-secret").Value(),
				tag:    f.TextField("tag").Value(),
				mode:   tailscaleModeChoices[f.RadioField("mode").Selected()],
			}
		}
	})
	if enabled {
		m.form.SetActionButton("Disable", func() tea.Msg { return tailscaleDisableSubmitMsg{} })
	}
	m.form.OnCancel(requestTailscaleFormClose)

	return m
}

func requestTailscaleFormClose(f *Form) tea.Cmd {
	return func() tea.Msg { return TailscaleFormCloseMsg{} }
}

// listenTailscaleStatus relays one waiting status from the enable task; the
// channel closing (task finished) ends the relay without a message.
func listenTailscaleStatus(ch chan string) tea.Cmd {
	return func() tea.Msg {
		status, ok := <-ch
		if !ok {
			return nil
		}
		return tailscaleStatusMsg(status)
	}
}
