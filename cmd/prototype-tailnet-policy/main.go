// PROTOTYPE - throwaway. Ticket 01 of .scratch/tailnet-policy-merge: the
// scenario switcher and the phase-one tagOwners declaration screen (ADR-0017).
// Not production code; lives on the prototype/tailnet-policy branch.
//
// Run: go run ./cmd/prototype-tailnet-policy
//
// Phase one is deliberately identical for every fixture: Once cannot read the
// Tailnet Policy before a credential exists, which is why both shapes of the
// entry are shown. Later phases (tickets 02-04) read the selected fixture.
package main

import (
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/once/internal/mouse"
	"github.com/basecamp/once/internal/ui"
)

const (
	tagOwnersLine  = `"tag:once": ["autogroup:admin"],`
	tagOwnersBlock = "\"tagOwners\": {\n\t" + tagOwnersLine + "\n},"

	policyEditorURL = "https://login.tailscale.com/admin/acls"
	oauthClientsURL = "https://login.tailscale.com/admin/settings/oauth"
	copyTarget      = "copy:tagowners"
	continueTarget  = "continue"
)

func main() {
	if _, err := tea.NewProgram(model{}).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type screen int

const (
	screenScenario screen = iota
	screenTagOwners
	screenCredential
	screenDiff
	screenNext
)

type (
	credSubmittedMsg struct{}
	credCancelMsg    struct{}
)

var (
	copyKey = ui.WithHelp(ui.NewKeyBinding("ctrl+p"), "ctrl+p", "copy")
	quitKey = ui.NewKeyBinding("ctrl+c", "q")
)

type model struct {
	screen        screen
	choice        int
	focused       int // 0 = copy, 1 = continue, on the screens that have both
	copied        bool
	skipped       bool
	width, height int
	form          ui.Form
	topology      *ui.RadioField

	// Diff screen state, built on entry by enterDiff.
	vp            viewport.Model
	mergedDoc     string
	addedSections []string
	addedLines    int
	addedAt       []int // diff line indices of the additions, for the scrollbar markers
	diffErr       error
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.sizeViewport()
		if m.screen == screenCredential {
			m.form, _ = m.form.Update(formSize(msg))
		}
		return m, nil

	case credSubmittedMsg:
		return m.enterDiff(), nil

	case credCancelMsg:
		m.screen = screenTagOwners
		m.focused = 0
		return m, nil

	case tea.MouseWheelMsg:
		if m.screen == screenDiff {
			m.vp, _ = m.vp.Update(msg)
		}
		return m, nil

	case tea.MouseClickMsg:
		ms := msg.Mouse()
		return m.handleClick(mouse.Resolve(ms.X, ms.Y))

	case tea.KeyPressMsg:
		if key.Matches(msg, quitKey) {
			return m, tea.Quit
		}
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) View() tea.View {
	var content string
	switch m.screen {
	case screenScenario:
		content = m.scenarioView()
	case screenTagOwners:
		content = m.tagOwnersView()
	case screenCredential:
		content = m.credentialView()
	case screenDiff:
		content = m.diffView()
	case screenNext:
		content = m.nextView()
	}
	v := tea.NewView(mouse.Sweep(lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	return v
}

// Private

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch m.screen {
	case screenScenario:
		switch msg.String() {
		case "up", "k":
			m.choice = (m.choice + len(scenarios) - 1) % len(scenarios)
		case "down", "j", "tab":
			m.choice = (m.choice + 1) % len(scenarios)
		case "enter":
			m.screen = screenTagOwners
		}
		return m, nil

	case screenTagOwners:
		switch {
		case key.Matches(msg, copyKey):
			return m.copyTagOwners()
		case msg.String() == "tab", msg.String() == "shift+tab", msg.String() == "left", msg.String() == "right":
			m.focused = 1 - m.focused
		case msg.String() == "enter":
			if m.focused == 0 {
				return m.copyTagOwners()
			}
			return m.enterCredential()
		case msg.String() == "s":
			// The skip path: a tag that already exists needs no verification -
			// a working tagged credential is itself the proof.
			m.skipped = true
			return m.enterCredential()
		case msg.String() == "esc":
			m.screen = screenScenario
		}
		return m, nil

	case screenCredential:
		switch msg.String() {
		case "esc":
			m.screen = screenTagOwners
			m.skipped = false
			m.focused = 0
			return m, nil
		case "ctrl+t":
			// ctrl, not bare t: the text fields below need the letter.
			m.topology.Cycle()
			return m, nil
		}
		var cmd tea.Cmd
		m.form, cmd = m.form.Update(msg)
		return m, cmd

	case screenDiff:
		switch {
		case key.Matches(msg, copyKey):
			return m.copyMerged()
		case msg.String() == "tab", msg.String() == "shift+tab", msg.String() == "left", msg.String() == "right":
			m.focused = 1 - m.focused
		case msg.String() == "enter":
			if m.focused == 0 {
				return m.copyMerged()
			}
			m.screen = screenNext
		case msg.String() == "esc":
			return m.enterCredential()
		default:
			m.vp, _ = m.vp.Update(msg)
		}
		return m, nil

	case screenNext:
		switch msg.String() {
		case "esc":
			// The nothing-to-add outcome never showed a diff, so esc returns
			// to the credential form instead.
			if len(m.addedSections) == 0 {
				return m.enterCredential()
			}
			return m.enterDiff(), nil
		case "enter":
			m.screen = screenScenario
			m.skipped = false
		}
		return m, nil
	}
	return m, nil
}

func (m model) handleClick(target string) (tea.Model, tea.Cmd) {
	if m.screen == screenScenario {
		for i := range scenarios {
			if target == scenarioTarget(i) {
				m.choice = i
				m.screen = screenTagOwners
				return m, nil
			}
		}
	}
	if m.screen == screenTagOwners {
		switch target {
		case copyTarget:
			return m.copyTagOwners()
		case continueTarget:
			return m.enterCredential()
		}
	}
	if m.screen == screenCredential {
		if target == "topology" {
			m.topology.Cycle()
			return m, nil
		}
		var cmd tea.Cmd
		m.form, cmd = m.form.Update(ui.MouseEvent{Target: target, IsClick: true})
		return m, cmd
	}
	if m.screen == screenDiff {
		switch target {
		case copyTarget:
			return m.copyMerged()
		case continueTarget:
			m.screen = screenNext
			return m, nil
		}
	}
	return m, nil
}

// copyTagOwners is the single copy path: the visible control and ctrl+p both
// land here.
// ponytail: one payload (the whole block); if testing shows line-case users
// pasting doubles, add a second copy target for the bare line.
func (m model) copyTagOwners() (tea.Model, tea.Cmd) {
	m.copied = true
	return m, tea.SetClipboard(tagOwnersBlock)
}

// copyMerged copies the whole Merged Policy document - the paste payload -
// never the diff rendering.
func (m model) copyMerged() (tea.Model, tea.Cmd) {
	m.copied = true
	return m, tea.SetClipboard(m.mergedDoc)
}

// enterCredential shows the Runtime Credential form, built fresh so the first
// field is focused and empty on every visit.
func (m model) enterCredential() (tea.Model, tea.Cmd) {
	m.screen = screenCredential
	m.form, m.topology = newCredentialForm()
	m.form, _ = m.form.Update(formSize(tea.WindowSizeMsg{Width: m.width, Height: m.height}))
	return m, m.form.Init()
}

// enterDiff computes the Merged Policy for the selected fixture and rebuilds
// the diff viewport.
func (m model) enterDiff() model {
	m.screen = screenDiff
	m.focused = 0
	m.copied = false

	original, merged, added, err := mergePolicy(scenarios[m.choice].policy)
	m.diffErr = err
	if err != nil {
		return m
	}
	m.mergedDoc = merged
	m.addedSections = added

	// Nothing to add: no diff to review, so no diff screen - straight to the
	// validate outcome (ticket 03).
	if len(added) == 0 {
		m.screen = screenNext
		return m
	}

	addedStyle := lipgloss.NewStyle().Foreground(ui.Colors.Success)
	lines := diffLines(original, merged)
	m.addedLines = 0
	m.addedAt = nil
	rendered := make([]string, len(lines))
	for i, line := range lines {
		text := strings.ReplaceAll(line.text, "\t", "    ")
		if line.added {
			m.addedAt = append(m.addedAt, i)
			m.addedLines++
			rendered[i] = addedStyle.Render("+ " + text)
		} else {
			rendered[i] = muted("  " + text)
		}
	}

	m.vp = viewport.New()
	m.vp.SetContent(strings.Join(rendered, "\n"))
	m.sizeViewport()
	// Open scrolled to the first addition, with a little context above it -
	// on a long policy the changes are usually near the bottom.
	if len(m.addedAt) > 0 {
		m.vp.SetYOffset(max(0, m.addedAt[0]-3))
	}
	return m
}

// scrollbar draws a thumb-on-track bar for the diff viewport, or nothing when
// the whole diff already fits. Added lines project onto the track as green
// markers, like an editor's overview ruler, so they are findable from
// anywhere in a long policy.
func (m model) scrollbar() string {
	total, height := m.vp.TotalLineCount(), m.vp.Height()
	if total <= height {
		return ""
	}
	thumb := max(1, height*height/total)
	pos := int(float64(height-thumb)*m.vp.ScrollPercent() + 0.5)

	markers := make(map[int]bool, len(m.addedAt))
	for _, idx := range m.addedAt {
		markers[min(idx*height/total, height-1)] = true
	}

	track := muted("│")
	thumbCell := lipgloss.NewStyle().Foreground(ui.Colors.Primary).Render("┃")
	markerCell := lipgloss.NewStyle().Foreground(ui.Colors.Success).Render("▐")
	markerThumbCell := lipgloss.NewStyle().Foreground(ui.Colors.Success).Render("┃")
	cells := make([]string, height)
	for i := range cells {
		inThumb := i >= pos && i < pos+thumb
		switch {
		case markers[i] && inThumb:
			cells[i] = markerThumbCell
		case markers[i]:
			cells[i] = markerCell
		case inThumb:
			cells[i] = thumbCell
		default:
			cells[i] = track
		}
	}
	return strings.Join(cells, "\n")
}

func (m *model) sizeViewport() {
	m.vp.SetWidth(72)
	m.vp.SetHeight(max(3, m.height-15))
}

func (m model) scenarioView() string {
	intro := muted("Pick the canned Tailnet Policy this run pretends your tailnet has.\n" +
		"Phase one looks the same for all of them - Once cannot read the\n" +
		"policy yet - but every later phase reads this selection.")

	var lines []string
	for i, s := range scenarios {
		cursor, style := "  ", lipgloss.NewStyle()
		if i == m.choice {
			cursor = "> "
			style = style.Foreground(ui.Colors.Focused).Bold(true)
		}
		row := style.Render(cursor+s.name) + "  " + muted(s.note)
		lines = append(lines, mouse.Mark(scenarioTarget(i), row))
	}

	return box().Render(lipgloss.JoinVertical(lipgloss.Center,
		title("Choose a Tailnet Policy fixture"),
		lipgloss.JoinVertical(lipgloss.Left,
			intro, "", strings.Join(lines, "\n"),
			helpLine("↑/↓ choose", "enter continue", "q quit"))))
}

func (m model) tagOwnersView() string {
	num := lipgloss.NewStyle().Bold(true).Foreground(ui.Colors.Primary)

	editorLink := lipgloss.NewStyle().Foreground(ui.Colors.Primary).Underline(true).
		Hyperlink(policyEditorURL).Render(policyEditorURL)

	steps := lipgloss.JoinVertical(lipgloss.Left,
		num.Render("1. Copy this entry"),
		policyBlock(tagOwnersBlock),
		muted("   already have tagOwners? add just this line inside it:"),
		policyBlock(tagOwnersLine),
		"",
		num.Render("2. Paste it")+muted(" in the policy editor: ")+editorLink,
		"",
		num.Render("3. Continue")+muted(" - done when tag:once is in the tag picker"),
	)

	copy := m.copyControl("Copy to clipboard")
	buttons := lipgloss.JoinHorizontal(lipgloss.Top, copy,
		lipgloss.PlaceHorizontal(lipgloss.Width(steps)-lipgloss.Width(copy),
			lipgloss.Right, m.continueButton()))

	return boxTight().Render(lipgloss.JoinVertical(lipgloss.Center,
		title("Need tagOwners in Policy File"),
		lipgloss.JoinVertical(lipgloss.Left, steps, "", buttons,
			helpLine("tab focus", "enter press", "ctrl+p copy", "s skip - tag exists", "esc fixtures"))))
}

func (m model) continueButton() string {
	return mouse.Mark(continueTarget,
		ui.Styles.Focus(ui.Styles.Button, m.focused == 1).Render("Continue"))
}

func (m model) credentialView() string {
	num := lipgloss.NewStyle().Bold(true).Foreground(ui.Colors.Primary)
	oauthLink := lipgloss.NewStyle().Foreground(ui.Colors.Primary).Underline(true).
		Hyperlink(oauthClientsURL).Render(oauthClientsURL)

	// The scope list follows the topology in step 1, live: Services needs the
	// extra `services` scope (ADR-0015), and both topologies carry the
	// policy-read pair from ADR-0017.
	scopes := "auth_keys:write, devices:core:write, policy_file:read,\n   devices:posture_attributes:read"
	if m.topology != nil && m.topology.Selected() == 1 {
		scopes = "auth_keys:write, devices:core:write, services:write,\n   policy_file:read, devices:posture_attributes:read"
	}

	steps := lipgloss.JoinVertical(lipgloss.Left,
		num.Render("1. Select your topology")+muted(" - ctrl+t or click to switch:"),
		"   "+mouse.Mark("topology", m.topology.View()),
		"",
		num.Render("2. Navigate to")+muted(": ")+oauthLink,
		"",
		num.Render("3. Create a new OAuth client")+muted(" with these scopes:"),
		"   "+lipgloss.NewStyle().Foreground(ui.Colors.Primary).Render(scopes),
		"",
		num.Render("4. Enter its values below")+muted(":"),
	)

	return boxTight().Render(lipgloss.JoinVertical(lipgloss.Center,
		title("Runtime Credential"),
		lipgloss.JoinVertical(lipgloss.Left, steps, rightAlignButtons(m.form.View()))))
}

func (m model) diffView() string {
	if m.diffErr != nil {
		return boxTight().Render(lipgloss.JoinVertical(lipgloss.Center,
			title("Review the Merged Policy"),
			lipgloss.NewStyle().Foreground(ui.Colors.Error).Render("Could not merge: "+m.diffErr.Error()),
			helpLine("esc back")))
	}

	summary := lipgloss.NewStyle().Foreground(ui.Colors.Success).Bold(true).
		Render(fmt.Sprintf("+%d lines", m.addedLines)) +
		muted(" · adds "+strings.Join(m.addedSections, ", "))
	if len(m.addedSections) == 0 {
		summary = lipgloss.NewStyle().Foreground(ui.Colors.Success).Bold(true).
			Render("Nothing to add") + muted(" - every section is already in your policy")
	}

	num := lipgloss.NewStyle().Bold(true).Foreground(ui.Colors.Primary)
	editorLink := lipgloss.NewStyle().Foreground(ui.Colors.Primary).Underline(true).
		Hyperlink(policyEditorURL).Render(policyEditorURL)
	steps := lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.JoinHorizontal(lipgloss.Center,
			num.Render("1. Copy the merged file"), "  ",
			m.copyControl("Copy Policy File to Clipboard")),
		num.Render("2. Open the editor")+muted(": ")+editorLink+muted(" - select all"),
		num.Render("3. Paste over the whole file")+muted(", save, then Continue"),
	)

	scroll := fmt.Sprintf("↑/↓ scroll %.0f%%", m.vp.ScrollPercent()*100)
	body := lipgloss.JoinHorizontal(lipgloss.Top, m.vp.View(), " ", m.scrollbar())
	return boxTight().Render(lipgloss.JoinVertical(lipgloss.Center,
		title("Review the Merged Policy"),
		lipgloss.JoinVertical(lipgloss.Left,
			summary, body, steps,
			lipgloss.PlaceHorizontal(lipgloss.Width(body), lipgloss.Right, m.continueButton()),
			helpLine(scroll, "tab focus", "enter press", "ctrl+p copy", "esc back"))))
}

func (m model) copyControl(label string) string {
	style := ui.Styles.Focus(ui.Styles.ButtonPrimary, m.focused == 0)
	if m.copied {
		style = style.Foreground(ui.Colors.Success)
		label = "Copied ✓"
	}
	return mouse.Mark(copyTarget, style.Render(label))
}

// nextView is the "Validate Policy & Install" outcome, canned per fixture.
// Validation that cannot refuse is decoration (ADR-0017), so two of the four
// end states stop the install.
func (m model) nextView() string {
	success := lipgloss.NewStyle().Bold(true).Foreground(ui.Colors.Success)
	warning := lipgloss.NewStyle().Bold(true).Foreground(ui.Colors.Warning)
	failure := lipgloss.NewStyle().Bold(true).Foreground(ui.Colors.Error)

	var head, body string
	switch scenarios[m.choice].outcome {
	case outcomeWarn:
		head = warning.Render("⚠ funnel nodeAttr missing - installing anyway")
		body = "Only Tailscale Funnel is affected: it is opt-in and temporary.\n" +
			"Add the nodeAttrs section before using `once funnel`.\n\n" +
			"Installing - the proxy joins the tailnet as tag:once."
	case outcomeRefuse:
		head = failure.Render("✗ Install refused")
		body = "The 443 grant and autoApprovers.services are missing.\n" +
			"Without the grant, Hosts cannot reach Applications on port 443.\n" +
			"Without autoApprovers.services, every Service waits for manual\n" +
			"approval and Boot times out.\n\n" +
			"Apply the Merged Policy, then re-run. Nothing was installed."
	case outcomeStale:
		head = failure.Render("✗ Tailnet Policy changed since the merge")
		body = "Someone edited the policy after this Merged Policy was\n" +
			"generated - it no longer matches what you reviewed.\n\n" +
			"Nothing was installed. Review the diff again and re-run."
	default:
		head = success.Render("✓ Policy validated")
		body = "Every section Once needs is in the Tailnet Policy.\n" +
			"Installing - the proxy joins the tailnet as tag:once."
		if len(m.addedSections) == 0 {
			head = success.Render("✓ Nothing to add")
			body = "Your policy already has every section - no diff to review.\n" +
				"Installing - the proxy joins the tailnet as tag:once."
		}
	}

	return box().Render(lipgloss.JoinVertical(lipgloss.Center,
		title("Validate Policy & Install"),
		lipgloss.JoinVertical(lipgloss.Left, head, "", muted(body),
			helpLine("enter start over", "esc back", "q quit"))))
}

// Helpers

// newCredentialForm mirrors the real enable form's credential fields
// (tailscale_form.go). Values are stand-ins - nothing probes Tailscale here.
// The topology radio lives outside the form, as step 1 above it, so it comes
// back separately.
func newCredentialForm() (ui.Form, *ui.RadioField) {
	secretField := ui.NewTextField("tskey-client-...")
	secretField.SetEchoPassword()
	tagField := ui.NewTextField("tag:once")
	tagField.SetValue("tag:once")
	topologyField := ui.NewRadioField("Nodes", "Services")

	form := ui.NewForm("Continue",
		ui.FormItem{Key: "client-id", Label: "Client ID", Field: ui.NewTextField("K123456CNTRL"), Required: true},
		ui.FormItem{Key: "client-secret", Label: "Client Secret", Field: secretField, Required: true},
		ui.FormItem{Key: "tag", Label: "Tag", Field: tagField, Required: true},
	)
	form.OnSubmit(func(f *ui.Form) tea.Cmd {
		return func() tea.Msg { return credSubmittedMsg{} }
	})
	form.OnCancel(func(f *ui.Form) tea.Cmd {
		return func() tea.Msg { return credCancelMsg{} }
	})
	return form, topologyField
}

// formSize shrinks the terminal size by the surrounding box chrome, so the
// form's inputs fit inside it at the 80-column floor.
func formSize(msg tea.WindowSizeMsg) tea.WindowSizeMsg {
	msg.Width -= 8
	return msg
}

// rightAlignButtons pushes a form's trailing button block to its right edge,
// so Continue sits in the bottom-right corner on every screen.
func rightAlignButtons(view string) string {
	lines := strings.Split(view, "\n")
	w := lipgloss.Width(view)
	start := max(0, len(lines)-3)

	// The form pads every line to its width; strip that, then shift the whole
	// block by one shared amount so the rows stay aligned with each other.
	block := 0
	for i := start; i < len(lines); i++ {
		lines[i] = strings.TrimRight(lines[i], " ")
		block = max(block, lipgloss.Width(lines[i]))
	}
	pad := strings.Repeat(" ", max(0, w-block))
	for i := start; i < len(lines); i++ {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}

func scenarioTarget(i int) string {
	return fmt.Sprintf("scenario:%d", i)
}

func policyBlock(s string) string {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(ui.Colors.Primary).
		Padding(0, 2).
		Render(strings.ReplaceAll(s, "\t", "    "))
}

func box() lipgloss.Style {
	return boxTight().Padding(1, 2)
}

// boxTight keeps the tallest screen inside the 80x24 floor the real app
// enforces.
func boxTight() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.Colors.Border).
		Padding(0, 2)
}

func title(s string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(ui.Colors.Primary).MarginBottom(1).Render(s)
}

func muted(s string) string {
	return lipgloss.NewStyle().Foreground(ui.Colors.Muted).Render(s)
}

func helpLine(bindings ...string) string {
	return lipgloss.NewStyle().MarginTop(1).Foreground(ui.Colors.Muted).Render(strings.Join(bindings, " · "))
}
