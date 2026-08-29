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

// tailscaleTopologyChoices maps the topology radio's option order to
// settings values (ADR-0015); nodes first, since that is the fresh-enable
// default.
var tailscaleTopologyChoices = []string{docker.TailnetTopologyNodes, docker.TailnetTopologyServices}

const tailscaleFormTextWidth = 72

var (
	tailscaleFormCloseKey = WithHelp(NewKeyBinding("esc"), "esc", "close")
	tailscaleCopyKey      = WithHelp(NewKeyBinding("ctrl+p"), "ctrl+p", "copy policy snippet")
)

type TailscaleFormCloseMsg struct{}

type tailscaleEnableSubmitMsg struct {
	id, secret, tag, mode, topology string
}

type tailscaleDisableSubmitMsg struct{}

type tailscaleFormFinishedMsg struct {
	err      error
	warnings []string
}

type tailscaleStatusMsg string

// TailscaleForm is the dashboard-global overlay for enabling/disabling
// Tailscale. Enabling runs the same flow as the CLI: probe the pasted OAuth
// credential and boot the requested tailnet topology. The credential's tag
// must be defined in the tailnet policy first; ctrl+p copies the required
// snippet for whichever topology is currently selected.
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
	var loadErr error
	if enabled {
		settings, loadErr = ns.Tailscale().LoadSettings(context.Background())
	}
	return newTailscaleForm(ns, enabled, settings, loadErr)
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
			return m, tea.SetClipboard(m.copyPayload())
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

// copyPayload is what ctrl+p carries: after a refused enable the fix is the
// Merged Policy, so that is what the copy control holds; the selected
// topology's static snippet is the payload in every other state.
func (m TailscaleForm) copyPayload() string {
	var refused *docker.PolicyRefusedError
	if errors.As(m.err, &refused) {
		return refused.Review.Merged
	}
	facts := tailscaleFactsFor(selectedTailscaleTopology(m.form))
	return facts.PolicySnippet(m.form.TextField("tag").Value())
}

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
			Topology:     msg.topology,
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
		var refused *docker.PolicyRefusedError
		switch {
		case errors.As(m.err, &refused):
			parts = append(parts, lipgloss.NewStyle().Foreground(Colors.Muted).Render("ctrl+p copies the Merged Policy - apply it, then enable again."))
		case errors.Is(m.err, docker.ErrTagNotInPolicy):
			parts = append(parts, lipgloss.NewStyle().Foreground(Colors.Muted).Render("ctrl+p copies the required policy snippet."))
		}
		parts = append(parts, "")
	} else if len(m.warnings) > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(Colors.Warning).Width(tailscaleFormTextWidth).Render(strings.Join(m.warnings, "\n")), "")
	}

	intro := "Tailscale is enabled. Enable re-applies the settings below\n(changing the topology, mode, credentials, or tag rebuilds it)."
	if !m.enabled {
		intro = tailscaleFactsFor(selectedTailscaleTopology(m.form)).SetupIntro
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
// requiring them would leave neither action able to submit. loadErr, when
// set, is the facade's own read failure (ErrOlderOnceSetup for a label that
// predates ADR-0015) shown verbatim instead of the usual setup intro.
func newTailscaleForm(ns *docker.Namespace, enabled bool, settings docker.TailscaleSettings, loadErr error) TailscaleForm {
	m := TailscaleForm{
		namespace: ns,
		enabled:   enabled,
		err:       loadErr,
		help:      NewHelp(),
		progress:  NewProgress(0, Colors.Border),
	}
	m.help.SetBindings([]key.Binding{tailscaleCopyKey, tailscaleFormCloseKey})

	topology := settings.Topology
	if topology == "" {
		topology = docker.TailnetTopologyNodes
	}

	clientIDField := NewTextField("K123456CNTRL")
	clientIDField.SetValue(settings.ClientID)

	clientSecretField := NewTextField("tskey-client-...")
	clientSecretField.SetEchoPassword()
	clientSecretField.SetValue(settings.ClientSecret)

	defaultTag := tailscaleFactsFor(topology).DefaultSettings.Tag
	tagField := NewTextField(defaultTag)
	if enabled {
		tagField.SetValue(settings.Tag)
	} else {
		tagField.SetValue(defaultTag)
	}

	topologyField := NewRadioField(tailscaleTopologyLabels()...)
	topologyField.SetSelected(max(slices.Index(tailscaleTopologyChoices, topology), 0))

	items := []FormItem{
		{Key: "client-id", Label: "Client ID", Field: clientIDField, Required: !enabled},
		{Key: "client-secret", Label: "Client Secret", Field: clientSecretField, Required: !enabled},
		{Key: "tag", Label: "Tag", Field: tagField, Required: !enabled},
		{Key: "topology", Label: "Topology", Field: topologyField},
	}
	if choices := tailscaleFactsFor(topology).ModeChoices; choices != nil {
		items = append(items, newTailscaleModeItem(choices, settings.Mode))
	}

	m.form = NewForm("Enable", items...)
	// The Mode radio only exists for a topology whose facts offer mode
	// choices (Services, ADR-0015): every rebuild - a keypress or a click on
	// the topology radio - reconciles its presence with whatever is
	// currently selected.
	m.form.OnRebuild(func(f *Form) {
		facts := tailscaleFactsFor(selectedTailscaleTopology(*f))
		switch {
		case facts.ModeChoices != nil && !formHasItem(*f, "mode"):
			f.AppendItems(newTailscaleModeItem(facts.ModeChoices, ""))
		case facts.ModeChoices == nil && formHasItem(*f, "mode"):
			f.RemoveItem("mode")
		}
	})

	m.form.OnSubmit(func(f *Form) tea.Cmd {
		return func() tea.Msg {
			topology := selectedTailscaleTopology(*f)
			mode := ""
			if formHasItem(*f, "mode") {
				mode = tailscaleFactsFor(topology).ModeChoices[f.RadioField("mode").Selected()]
			}
			return tailscaleEnableSubmitMsg{
				id:       f.TextField("client-id").Value(),
				secret:   f.TextField("client-secret").Value(),
				tag:      f.TextField("tag").Value(),
				mode:     mode,
				topology: topology,
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

// tailscaleFactsFor returns topology's facts, falling back to the nodes
// topology's for a corrupt or unrecognized value - the form must never crash
// on bad stored state, and nodes (no mode, the fresh-enable default) is the
// safe shape to fall back to.
func tailscaleFactsFor(topology string) docker.TopologyFacts {
	facts, err := docker.TailscaleTopologyFacts(topology)
	if err != nil {
		facts, _ = docker.TailscaleTopologyFacts(docker.TailnetTopologyNodes)
	}
	return facts
}

// tailscaleTopologyLabels renders tailscaleTopologyChoices as the radio's
// display labels (e.g. "nodes" -> "Nodes"), so the option text is derived
// from the docker package's own constants rather than typed out twice.
func tailscaleTopologyLabels() []string {
	labels := make([]string, len(tailscaleTopologyChoices))
	for i, choice := range tailscaleTopologyChoices {
		labels[i] = capitalizeFirst(choice)
	}
	return labels
}

// selectedTailscaleTopology reads the form's topology radio field.
func selectedTailscaleTopology(f Form) string {
	return tailscaleTopologyChoices[f.RadioField("topology").Selected()]
}

// formHasItem reports whether f has a field with this key, without the
// panic Form.RadioField/TextField raise for an absent one - needed here
// because the Mode field's very presence is conditional.
func formHasItem(f Form, key string) bool {
	for _, item := range f.items {
		if item.Key == key {
			return true
		}
	}
	return false
}

// newTailscaleModeItem builds the Mode radio for a topology whose facts offer
// mode choices, preselecting storedMode when it names one of them and the
// first (default) choice otherwise.
func newTailscaleModeItem(choices []string, storedMode string) FormItem {
	labels := make([]string, len(choices))
	for i, choice := range choices {
		labels[i] = capitalizeFirst(choice)
	}
	field := NewRadioField(labels...)
	field.SetSelected(max(slices.Index(choices, storedMode), 0))
	return FormItem{Key: "mode", Label: "Mode", Field: field}
}

// capitalizeFirst upper-cases s's first byte, for turning a settings value
// (docker.TailscaleModeSidecar, TailnetTopologyNodes, ...) into the label a
// radio field displays, without typing that word out a second time.
func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
