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
	screenDiff
	screenNext
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

	// Diff screen state, built on entry by enterDiff.
	vp            viewport.Model
	mergedDoc     string
	addedSections []string
	addedLines    int
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
			return m.enterDiff(), nil
		case msg.String() == "s":
			// The skip path: a tag that already exists needs no verification -
			// a working tagged credential is itself the proof.
			m.skipped = true
			return m.enterDiff(), nil
		case msg.String() == "esc":
			m.screen = screenScenario
		}
		return m, nil

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
			m.screen = screenTagOwners
			m.skipped = false
			m.focused = 0
		default:
			m.vp, _ = m.vp.Update(msg)
		}
		return m, nil

	case screenNext:
		if msg.String() == "esc" {
			return m.enterDiff(), nil
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
			return m.enterDiff(), nil
		}
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

	addedStyle := lipgloss.NewStyle().Foreground(ui.Colors.Success)
	lines := diffLines(original, merged)
	m.addedLines = 0
	rendered := make([]string, len(lines))
	for i, line := range lines {
		text := strings.ReplaceAll(line.text, "\t", "    ")
		if line.added {
			m.addedLines++
			rendered[i] = addedStyle.Render("+ " + text)
		} else {
			rendered[i] = muted("  " + text)
		}
	}

	m.vp = viewport.New()
	m.vp.SetContent(strings.Join(rendered, "\n"))
	m.sizeViewport()
	return m
}

func (m *model) sizeViewport() {
	m.vp.SetWidth(74)
	m.vp.SetHeight(max(3, m.height-12))
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

	return boxTight().Render(lipgloss.JoinVertical(lipgloss.Center,
		title("Need tagOwners in Policy File"),
		lipgloss.JoinVertical(lipgloss.Left, steps, "", m.actionButtons(),
			helpLine("tab focus", "enter press", "ctrl+p copy", "s skip - tag exists", "esc fixtures"))))
}

// actionButtons is the Copy/Continue pair the tagOwners and diff screens
// share, with the focused one highlighted.
func (m model) actionButtons() string {
	return lipgloss.JoinHorizontal(lipgloss.Center,
		m.copyControl(),
		mouse.Mark(continueTarget,
			ui.Styles.Focus(ui.Styles.Button, m.focused == 1).Render("Continue")))
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

	scroll := fmt.Sprintf("↑/↓ scroll %.0f%%", m.vp.ScrollPercent()*100)
	return boxTight().Render(lipgloss.JoinVertical(lipgloss.Center,
		title("Review the Merged Policy"),
		lipgloss.JoinVertical(lipgloss.Left,
			summary, "", m.vp.View(), "", m.actionButtons(),
			helpLine(scroll, "tab focus", "enter press", "ctrl+p copy", "esc back"))))
}

func (m model) copyControl() string {
	label := "Copy to clipboard"
	style := ui.Styles.Focus(ui.Styles.ButtonPrimary, m.focused == 0)
	if m.copied {
		style = style.Foreground(ui.Colors.Success)
		label = "Copied ✓"
	}
	return mouse.Mark(copyTarget, style.Render(label))
}

func (m model) nextView() string {
	s := scenarios[m.choice]

	how := "declared the tag and continued"
	if m.skipped {
		how = "skipped - the tag already exists (no verification; a working\ntagged credential is itself the proof)"
	}

	preview := s.policy
	if lines := strings.Split(preview, "\n"); len(lines) > 6 {
		preview = strings.Join(lines[:6], "\n") + "\n…"
	}

	body := lipgloss.JoinVertical(lipgloss.Left,
		muted("Phase three - Validate Policy & Install - is ticket 03 and not\nbuilt yet. It will read this state:"),
		"",
		lipgloss.NewStyle().Bold(true).Render("Fixture   ")+s.name+"  "+muted(fmt.Sprintf("(%d lines)", strings.Count(s.policy, "\n")+1)),
		lipgloss.NewStyle().Bold(true).Render("Phase one ")+muted(how),
		"",
		policyBlock(preview),
		helpLine("esc back", "q quit"))

	return box().Render(lipgloss.JoinVertical(lipgloss.Center, title("End of the prototype (so far)"), body))
}

// Helpers

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
