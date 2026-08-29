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

// tailscaleFormTextWidth keeps wrapped prose inside the credential
// screen's box at the 80-column floor: 68 content columns plus the box's
// borders and padding measure 78.
const tailscaleFormTextWidth = 68

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

// tailscaleScreen names which screen of the enable flow the overlay shows.
// A fresh enable opens on the tagOwners screen (phase one of ADR-0017), the
// credential form follows, and a refused enable lands on the Merged Policy
// diff. An enabled install goes straight to the credential form.
type tailscaleScreen int

const (
	tailscaleScreenCredential tailscaleScreen = iota
	tailscaleScreenTagOwners
	tailscaleScreenDiff
)

// TailscaleForm is the dashboard-global overlay for enabling/disabling
// Tailscale. Enabling runs the same flow as the CLI: probe the pasted OAuth
// credential, review the Tailnet Policy, and boot the requested tailnet
// topology. The credential's tag must be defined in the tailnet policy
// first (the tagOwners screen); ctrl+p copies the required snippet for
// whichever topology is currently selected, or the Merged Policy after a
// refused enable.
type TailscaleForm struct {
	namespace     *docker.Namespace
	enabled       bool
	screen        tailscaleScreen
	tagOwners     tagOwnersScreen
	diff          policyDiffScreen
	lastEnable    tailscaleEnableSubmitMsg
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
		m.diff, _ = m.diff.Update(msg)
		return m, nil

	case tea.KeyPressMsg:
		if m.running {
			return m, nil
		}
		if key.Matches(msg, tailscaleFormCloseKey) {
			return m.escape()
		}
		if m.screen != tailscaleScreenCredential {
			return m.updateScreen(msg)
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
		if m.screen != tailscaleScreenCredential {
			return m.updateScreen(msg)
		}

	case tea.MouseWheelMsg:
		return m.updateScreen(msg)

	case tagOwnersContinueMsg:
		m.screen = tailscaleScreenCredential
		return m, m.form.Init()

	case policyDiffContinueMsg:
		// The diff's step 3 is "save, then Continue": the Merged Policy has
		// been applied, so the same enable runs again.
		return m.Update(m.lastEnable)

	case tailscaleEnableSubmitMsg:
		m.lastEnable = msg
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
		// Every outcome lands on a screen that can show it: only a refusal
		// belongs on the diff, so any other finish - a stale stop, a
		// credential error, warn-and-proceed - returns to the credential
		// form, where m.err and m.warnings render. Without the reset, an
		// enable re-run from the diff's Continue would leave the stale diff
		// on screen, hiding the outcome.
		m.screen = tailscaleScreenCredential
		if msg.err != nil {
			m.err = msg.err
			// A refusal carries the review, so the fix is shown as the
			// Merged Policy diff rather than an error paragraph; esc from
			// there returns to the form, where the refusal message waits.
			var refused *docker.PolicyRefusedError
			if errors.As(msg.err, &refused) {
				m.screen = tailscaleScreenDiff
				m.diff = newPolicyDiffScreen(refused.Review, m.height)
				return m, nil
			}
			return m, m.form.Init()
		}
		// Shadowing warnings keep the form open so the user actually sees them.
		if len(msg.warnings) > 0 {
			m.warnings = msg.warnings
			return m, m.form.Init()
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

	if m.screen != tailscaleScreenCredential {
		return m, nil
	}
	var cmd tea.Cmd
	m.form, cmd = m.form.Update(msg)
	return m, cmd
}

func (m TailscaleForm) View() string {
	if m.running {
		body := m.progress.View()
		if m.status != "" {
			status := lipgloss.NewStyle().Foreground(Colors.Muted).Width(tailscaleFormTextWidth).Render(m.status)
			body = lipgloss.JoinVertical(lipgloss.Center, status, "", body)
		}
		return m.frame(body, false)
	}

	switch m.screen {
	case tailscaleScreenTagOwners:
		return m.tagOwners.View()
	case tailscaleScreenDiff:
		return m.diff.View()
	}

	// The setup steps are the screen's point, so they are the last thing to
	// go: when the full view overflows, the form tightens first - inline
	// radios, no separator or padding rows - and only when even that cannot
	// make room does the guidance collapse. The tightening also keeps the
	// steps up when a topology click grows the form (Services reveals the
	// Mode field), instead of popping them away mid-selection.
	// TestTailscaleForm_CredentialScreenFitsThe24RowFloor and
	// TestTailscaleForm_TopologyClickKeepsTheGuidanceVisible pin it.
	if v := m.frame(m.setupView(true, false), false); m.height == 0 || lipgloss.Height(v) <= m.height {
		return v
	}
	if v := m.frame(m.setupView(true, true), true); lipgloss.Height(v) <= m.height {
		return v
	}
	if v := m.frame(m.setupView(false, false), false); lipgloss.Height(v) <= m.height {
		return v
	}
	return m.frame(m.setupView(false, true), true)
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

// setupView is the credential screen's body. guidance carries the setup
// steps (or the enabled-state intro), dropped when the terminal is too short
// for them; compact tightens the form itself for the 24-row floor.
func (m TailscaleForm) setupView(guidance, compact bool) string {
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

	if guidance {
		parts = append(parts, m.guidance(), "")
	}

	form := m.form
	form.SetCompact(compact)
	parts = append(parts, form.View())

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// guidance is the credential screen's intro: numbered setup steps with a
// scope list that follows the topology selection live (fresh enable), or the
// re-enable explainer (enabled install).
func (m TailscaleForm) guidance() string {
	if m.enabled {
		return policyMuted("Tailscale is enabled. Enable re-applies the settings below\n(changing the topology, mode, credentials, or tag rebuilds it).")
	}

	facts := tailscaleFactsFor(selectedTailscaleTopology(m.form))
	num := policyStepStyle()
	scopes := lipgloss.NewStyle().Foreground(Colors.Primary).PaddingLeft(3).Width(tailscaleFormTextWidth).
		Render(strings.Join(facts.OAuthScopes, ", "))

	return lipgloss.JoinVertical(lipgloss.Left,
		num.Render("1. Select your topology")+policyMuted(" - the first field below"),
		"",
		num.Render("2. Navigate to")+policyMuted(": ")+policyLink(tailscaleOAuthClientsURL),
		"",
		num.Render("3. Create a new OAuth client")+policyMuted(" with these scopes:"),
		scopes,
		"",
		num.Render("4. Enter its values")+policyMuted(" and Continue:"),
	)
}

// frame wraps a credential-screen body in the overlay's box, title and help
// line; tight sheds the vertical padding and margins for the 24-row floor.
func (m TailscaleForm) frame(body string, tight bool) string {
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Colors.Border).
		Padding(1, 4)
	title := lipgloss.NewStyle().Bold(true).Foreground(Colors.Primary).MarginBottom(1)
	helpLine := lipgloss.NewStyle().MarginTop(1).Align(lipgloss.Center)
	if tight {
		boxStyle = boxStyle.Padding(0, 4)
		title = title.MarginBottom(0)
		helpLine = helpLine.MarginTop(0)
	}
	return boxStyle.Render(lipgloss.JoinVertical(lipgloss.Center,
		title.Render("Tailscale"), body, helpLine.Render(m.help.View())))
}

// escape is the overlay-wide esc gesture, screen by screen: the diff returns
// to the credential form (the refusal message waits there), a fresh enable's
// credential form returns to the tagOwners screen, and everything else
// closes the overlay.
func (m TailscaleForm) escape() (Component, tea.Cmd) {
	switch m.screen {
	case tailscaleScreenDiff:
		m.screen = tailscaleScreenCredential
		return m, m.form.Init()
	case tailscaleScreenCredential:
		if !m.enabled {
			m.screen = tailscaleScreenTagOwners
			return m, nil
		}
	}
	return m, func() tea.Msg { return TailscaleFormCloseMsg{} }
}

// updateScreen forwards msg to whichever policy screen is active.
func (m TailscaleForm) updateScreen(msg tea.Msg) (Component, tea.Cmd) {
	var cmd tea.Cmd
	switch m.screen {
	case tailscaleScreenTagOwners:
		m.tagOwners, cmd = m.tagOwners.Update(msg)
	case tailscaleScreenDiff:
		m.diff, cmd = m.diff.Update(msg)
	}
	return m, cmd
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

	// Topology leads: the guidance's step 1 selects it, and the scope list
	// in step 3 follows the selection live (ADR-0017 amendment).
	items := []FormItem{
		{Key: "topology", Label: "Topology", Field: topologyField},
		{Key: "client-id", Label: "Client ID", Field: clientIDField, Required: !enabled},
		{Key: "client-secret", Label: "Client Secret", Field: clientSecretField, Required: !enabled},
		{Key: "tag", Label: "Tag", Field: tagField, Required: !enabled},
	}
	if choices := tailscaleFactsFor(topology).ModeChoices; choices != nil {
		items = append(items, newTailscaleModeItem(choices, settings.Mode))
	}

	submitLabel := "Continue"
	if enabled {
		submitLabel = "Enable"
	}
	m.form = NewForm(submitLabel, items...)
	// Esc covers cancelling (the overlay routes it screen by screen), so a
	// Cancel button would be a second control for the same exit; the primary
	// action sits bottom-right on every screen of the flow.
	m.form.HideCancelButton()
	m.form.AlignButtonsRight()
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

	// A fresh enable opens on the tagOwners screen: the console will not
	// issue a tagged credential until the tag is in tagOwners, so no
	// credential-driven step can come first (ADR-0017).
	if !enabled {
		m.screen = tailscaleScreenTagOwners
		m.tagOwners = newTagOwnersScreen(defaultTag)
	}

	return m
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
