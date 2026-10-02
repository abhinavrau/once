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
	tailscaleHelpKey      = WithHelp(NewKeyBinding("f1"), "F1", "help")
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
// Both a fresh enable and a normal re-enable open on the tagOwners screen
// (phase one of ADR-0017, extended to re-enable so the tag stays editable in
// one place): the credential form follows, and a refused enable lands on the
// Merged Policy diff. An install whose stored settings failed to load
// (hasTagOwnersScreen false) skips straight to the credential form, which
// shows the load error instead.
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
// first (the tagOwners screen), which is where the entry to paste is
// copied from; a refused enable copies the Merged Policy from the diff
// screen.
type TailscaleForm struct {
	namespace *docker.Namespace
	enabled   bool
	screen    tailscaleScreen
	// hasTagOwnersScreen is false only when stored settings failed to load;
	// otherwise both a fresh enable and a re-enable start on the tagOwners
	// screen, so escape() and the initial screen choice share one flag.
	hasTagOwnersScreen bool
	// tag is the tagOwners screen's current value, carried here once the
	// user leaves it (Continue or skip) so the credential form - which no
	// longer has a tag field of its own - and the enable submission both
	// read the same value.
	tag           string
	tagOwners     tagOwnersScreen
	diff          policyDiffScreen
	popupHelp     *PopupHelp
	lastEnable    tailscaleEnableSubmitMsg
	form          Form
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

// CopyableError is the failed enable's message, for App's copy key. It
// reaches App through the screen hosting this overlay.
func (m TailscaleForm) CopyableError() string {
	return errorText(m.err)
}

func (m TailscaleForm) Init() tea.Cmd {
	if m.screen == tailscaleScreenTagOwners {
		return m.tagOwners.Init()
	}
	return m.form.Init()
}

func (m TailscaleForm) Update(msg tea.Msg) (Component, tea.Cmd) {
	if m.popupHelp != nil {
		switch msg.(type) {
		case PopupHelpCloseMsg:
			m.popupHelp = nil
			return m, nil
		case tea.KeyPressMsg, MouseEvent, tea.MouseWheelMsg:
			ph, cmd := m.popupHelp.Update(msg)
			m.popupHelp = &ph
			return m, cmd
		}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.progress = m.progress.SetWidth(m.width)
		m.form, _ = m.form.Update(msg)
		m.diff, _ = m.diff.Update(msg)
		m.tagOwners, _ = m.tagOwners.Update(msg)
		return m, nil

	case tea.KeyPressMsg:
		if m.running {
			return m, nil
		}
		if key.Matches(msg, tailscaleHelpKey) {
			title, content := m.helpContent()
			ph := NewPopupHelp(title, content, m.width, m.height)
			m.popupHelp = &ph
			return m, nil
		}
		if key.Matches(msg, tailscaleFormCloseKey) {
			return m.escape()
		}
		if m.screen != tailscaleScreenCredential {
			return m.updateScreen(msg)
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
		m.tag = msg.tag
		m.screen = tailscaleScreenCredential
		return m, m.form.Init()

	case policyDiffContinueMsg:
		// The diff's step 3 is "save, then Continue": the Merged Policy has
		// been applied, so the same enable runs again.
		return m.Update(m.lastEnable)

	case tailscaleEnableSubmitMsg:
		// The tag field lives on the tagOwners screen now, not this form, so
		// the submit callback never sets it; the current value is attached
		// here instead.
		msg.tag = m.tag
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
	view := m.view()
	if m.popupHelp != nil {
		return OverlayCenter(view, m.popupHelp.View(), m.width, m.height)
	}
	return view
}

func (m TailscaleForm) view() string {
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
	// make room does the guidance collapse. setupView reserves the absent
	// Mode field's rows, so the chosen tier depends on the terminal size
	// alone and a topology click changes the form's content, never its
	// layout. TestTailscaleForm_CredentialScreenFitsThe24RowFloor and
	// TestTailscaleForm_TopologySwitchNeverChangesTheLayoutTier pin it.
	// The frame's own padding is the last thing to go: every tier is measured
	// in the roomy frame, and only a terminal too short for even the tightest
	// tier gets the margin-free one.
	for _, tier := range []struct{ guidance, compact bool }{
		{true, false}, {true, true}, {false, false}, {false, true},
	} {
		if m.height == 0 || m.fits(tier.guidance, tier.compact, false) {
			return m.frame(m.setupView(tier.guidance, tier.compact), false)
		}
	}
	return m.frame(m.setupView(false, true), true)
}

// Private

// helpContent returns the F1 popup's title and body for whichever screen of
// the flow is currently active.
func (m TailscaleForm) helpContent() (string, string) {
	switch m.screen {
	case tailscaleScreenTagOwners:
		return "Need tagOwners in Policy File", tagOwnersHelpText
	case tailscaleScreenDiff:
		return "Review the Merged Policy", policyDiffHelpText
	default:
		return "Tailscale setup: Credentials", credentialHelpText
	}
}

// credentialHelpText is the credential screen's F1 popup body.
const credentialHelpText = `This screen collects the Tailscale OAuth client Once uses to manage this tailnet.

Topology chooses how Once exposes this server: Nodes joins it to the tailnet directly; Services publishes it through tailscaled's Services feature.

Mode appears under the Services topology and picks the tailscaled that publishes them. Sidecar (the default) boots a Once-managed tailscaled container: it needs no setup on the host, at the cost of one extra tailnet device. Host reuses the tailscaled already running on this host, saving that device slot - it must be logged into the credential's tailnet, tagged with your tag, and v1.86 or newer, which enable checks before touching anything.

Client ID and Client Secret come from an OAuth client you create in the Tailscale admin console (Settings -> OAuth clients), scoped to the tag declared on the previous screen.
`

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

// fits reports whether the credential screen fits the terminal at the given
// tier. setupView reserves the absent Mode field's rows, so the measured
// height is a function of the terminal size alone and the tier cannot change
// when a topology click reveals or removes the Mode field.
func (m TailscaleForm) fits(guidance, compact, tight bool) bool {
	return lipgloss.Height(m.frame(m.setupView(guidance, compact), tight)) <= m.height
}

// setupView is the credential screen's body. guidance carries the setup
// steps (or the enabled-state intro), dropped when the terminal is too short
// for them; compact tightens the form itself for the 24-row floor.
func (m TailscaleForm) setupView(guidance, compact bool) string {
	var parts []string
	if m.err != nil {
		parts = append(parts, lipgloss.NewStyle().Width(tailscaleFormTextWidth).Render(errorWithCopyHint(m.err)))
		var refused *docker.PolicyRefusedError
		switch {
		case errors.As(m.err, &refused):
			parts = append(parts, lipgloss.NewStyle().Foreground(Colors.Muted).Render("Continue to review the Merged Policy - apply it, then enable again."))
		case errors.Is(m.err, docker.ErrTagNotInPolicy) && m.hasTagOwnersScreen:
			parts = append(parts, lipgloss.NewStyle().Foreground(Colors.Muted).Render("esc returns to the tagOwners screen, where the entry can be copied."))
		}
		parts = append(parts, "")
	} else if len(m.warnings) > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(Colors.Warning).Width(tailscaleFormTextWidth).Render(strings.Join(m.warnings, "\n")), "")
	}

	form := m.form
	form.SetCompact(compact)

	// The Mode field only exists under Services (ADR-0007's selector); when
	// it is absent, blank rows of its exact height hold its place under
	// Topology, so a topology click swaps the field with reserved space
	// instead of moving the fields and buttons below it. Measured before the
	// prologues go in, so the difference is the Mode block alone.
	if !formHasItem(form, "mode") {
		worst := form
		worst.AppendItems(newTailscaleModeItem(tailscaleFactsFor(docker.TailnetTopologyServices).ModeChoices, ""))
		form.SetReservedRowsAfter("topology", lipgloss.Height(worst.View())-lipgloss.Height(form.View()))
	}

	// The setup steps interleave with the fields they explain: step 1 sits
	// above the Topology radio (Mode rides along under it), steps 2-4 above
	// the credential fields they lead to. An enabled install gets the
	// re-enable explainer instead.
	if guidance {
		if m.enabled {
			parts = append(parts, policyMuted("Tailscale is enabled. Enable re-applies the settings below\n(changing the topology, mode, or credentials rebuilds it; so does\nediting the tag on the previous screen)."), "")
		} else {
			form.SetItemPrologue("topology", policyStepStyle().Render("1. Select your topology"))
			form.SetItemPrologue("client-id", m.oauthSteps())
		}
	}

	parts = append(parts, form.View())

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// oauthSteps is the guidance's steps 2-4, rendered above the credential
// fields: the scope list follows the topology selection live, padded to the
// tallest topology's row count so a topology click never changes its height.
func (m TailscaleForm) oauthSteps() string {
	facts := tailscaleFactsFor(selectedTailscaleTopology(m.form))
	num := policyStepStyle()

	rows := tailscaleScopeRows(facts.OAuthScopes)
	for _, topology := range tailscaleTopologyChoices {
		for range len(tailscaleScopeRows(tailscaleFactsFor(topology).OAuthScopes)) - len(rows) {
			rows = append(rows, "")
		}
	}
	// Fixed width: the scope rows are the guidance's widest lines and differ
	// per topology, so an unpadded block would resize the box on a topology
	// click.
	scopes := lipgloss.NewStyle().PaddingLeft(3).Width(tailscaleFormTextWidth).Render(strings.Join(rows, "\n"))

	return lipgloss.JoinVertical(lipgloss.Left,
		num.Render("2. Navigate to")+policyMuted(": ")+linkedURL(tailscaleOAuthClientsURL),
		"",
		num.Render("3. Create a new OAuth client")+policyMuted(" with these scopes:"),
		scopes,
		"",
		num.Render("4. Enter its values")+policyMuted(" and Continue:"),
	)
}

// frame wraps a credential-screen body in the overlay's box and title; tight
// sheds the vertical padding and margins for the 24-row floor. No screen of
// this flow carries a help line of its own - the screen behind the overlay
// already renders one.
func (m TailscaleForm) frame(body string, tight bool) string {
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Colors.Border).
		Padding(1, 4)
	title := lipgloss.NewStyle().Bold(true).Foreground(Colors.Primary).MarginBottom(1)
	if tight {
		boxStyle = boxStyle.Padding(0, 4)
		title = title.MarginBottom(0)
	}
	return boxStyle.Render(lipgloss.JoinVertical(lipgloss.Center,
		title.Render("Tailscale setup: Credentials"), body))
}

// escape is the overlay-wide esc gesture, screen by screen: the diff returns
// to the credential form (the refusal message waits there), the credential
// form returns to the tagOwners screen (when there is one to return to), and
// everything else closes the overlay.
func (m TailscaleForm) escape() (Component, tea.Cmd) {
	switch m.screen {
	case tailscaleScreenDiff:
		m.screen = tailscaleScreenCredential
		return m, m.form.Init()
	case tailscaleScreenCredential:
		if m.hasTagOwnersScreen {
			m.screen = tailscaleScreenTagOwners
			// Back on the screen the tag is edited on, the cursor belongs in
			// the field again, exactly as it does when it first opens.
			var cmd tea.Cmd
			m.tagOwners, cmd = m.tagOwners.focusField()
			return m, cmd
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
		progress:  NewProgress(0, Colors.Border),
	}
	topology := settings.Topology
	if topology == "" {
		topology = docker.TailnetTopologyNodes
	}

	clientIDField := NewTextField("K123456CNTRL")
	clientIDField.SetValue(settings.ClientID)

	clientSecretField := NewTextField("tskey-client-...")
	clientSecretField.SetEchoPassword()
	clientSecretField.SetValue(settings.ClientSecret)

	// The tag is entered on the tagOwners screen, not here (moved so the
	// tagOwners entry the user pastes and the tag the credential is scoped
	// to can never drift apart). defaultTag is bare (no "tag:" prefix): the
	// field edits and displays the bare name(s); normalizeTag re-adds the
	// prefix wherever the value is actually consumed.
	defaultTag := stripTagPrefix(tailscaleFactsFor(topology).DefaultSettings.Tag)
	tag := defaultTag
	if enabled && settings.Tag != "" {
		tag = stripTagPrefix(settings.Tag)
	}
	m.tag = tag

	topologyField := NewRadioField(tailscaleTopologyLabels()...)
	topologyField.SetSelected(max(slices.Index(tailscaleTopologyChoices, topology), 0))

	// Topology leads: the guidance's step 1 selects it, and the scope list
	// in step 3 follows the selection live (ADR-0017 amendment). Mode sits
	// directly under it - the two select the topology together, and the
	// credential fields below belong to the later steps.
	items := []FormItem{
		{Key: "topology", Label: "Topology", Field: topologyField},
	}
	if choices := tailscaleFactsFor(topology).ModeChoices; choices != nil {
		items = append(items, newTailscaleModeItem(choices, settings.Mode))
	}
	items = append(items,
		FormItem{Key: "client-id", Label: "Client ID", Field: clientIDField, Required: !enabled},
		FormItem{Key: "client-secret", Label: "Client Secret", Field: clientSecretField, Required: !enabled},
	)

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
			f.InsertItemAfter("topology", newTailscaleModeItem(facts.ModeChoices, ""))
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
				mode:     mode,
				topology: topology,
			}
		}
	})
	if enabled {
		m.form.SetActionButton("Disable", func() tea.Msg { return tailscaleDisableSubmitMsg{} })
	}

	// Both a fresh enable and a normal re-enable open on the tagOwners
	// screen: the console will not issue a tagged credential until the tag
	// is in tagOwners, so no credential-driven step can come first
	// (ADR-0017). An install whose stored settings failed to load has
	// nothing to tag-check, so it skips straight to the credential form,
	// which shows the load error instead.
	m.tagOwners = newTagOwnersScreen(tag)
	m.hasTagOwnersScreen = loadErr == nil
	if m.hasTagOwnersScreen {
		m.screen = tailscaleScreenTagOwners
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

// tailscaleConsoleScopes maps the facts' API scope identifiers to the rows
// the admin console's credential form shows, in the console's own order. An
// empty console label is a companion grant the console checks automatically
// (per the trust-credentials reference, policy_file requires
// devices:posture_attributes and devices:core:read), so it is not a row the
// user selects.
var tailscaleConsoleScopes = []struct{ scope, console, access string }{
	{"policy_file:read", "General -> Policy File", "Read"},
	{"services:write", "General -> Services", "Write"},
	{"services:read", "General -> Services", "Read"},
	{"devices:core:write", "Devices -> Core", "Write"},
	{"auth_keys:write", "Keys -> Auth Keys", "Write"},
	{"devices:posture_attributes:read", "", ""},
}

// tailscaleScopeRows renders scopes as the admin console's checklist, two
// checkboxes per row to keep the guidance short, a white-on-blue check box
// marking each access level. A blank line separates the rows - adjacent rows
// would fuse the colored check cells into one solid bar. A scope the table
// does not know falls back to its raw identifier, so a new fact is never
// silently hidden.
func tailscaleScopeRows(scopes []string) []string {
	var cells []struct{ console, access string }
	matched := make(map[string]bool)
	for _, cs := range tailscaleConsoleScopes {
		if !slices.Contains(scopes, cs.scope) {
			continue
		}
		matched[cs.scope] = true
		if cs.console != "" {
			cells = append(cells, struct{ console, access string }{cs.console, cs.access})
		}
	}

	// Per-column label widths keep both columns aligned across rows while
	// staying inside the guidance's 68-column budget.
	columnWidth := [2]int{}
	for i, cell := range cells {
		columnWidth[i%2] = max(columnWidth[i%2], len(cell.console))
	}
	check := lipgloss.NewStyle().Background(Colors.Blue).Foreground(Colors.BrightWhite).Render(" ✓ ")
	render := func(i int) string {
		label := lipgloss.NewStyle().Foreground(Colors.Primary).Width(columnWidth[i%2] + 1)
		return label.Render(cells[i].console) + check + " " + cells[i].access
	}

	var rows []string
	for i := 0; i < len(cells); i += 2 {
		if len(rows) > 0 {
			rows = append(rows, "")
		}
		row := render(i)
		if i+1 < len(cells) {
			row = lipgloss.NewStyle().Width(columnWidth[0]+12).Render(row) + render(i+1)
		}
		rows = append(rows, row)
	}
	for _, scope := range scopes {
		if !matched[scope] {
			rows = append(rows, "", lipgloss.NewStyle().Foreground(Colors.Primary).Render(scope))
		}
	}
	return rows
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
