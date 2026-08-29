package ui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/once/internal/docker"
	"github.com/basecamp/once/internal/mouse"
)

// The tagOwners and Merged Policy screens of the Tailscale enable flow,
// lifted from the validated prototype (ADR-0017 amendment). Both are hosted
// by TailscaleForm; because Once never writes the Tailnet Policy, the paste
// is the critical path, so copying is a first-class control on each: a
// visible, clickable button and ctrl+p share one copy path.

const (
	tailscalePolicyEditorURL = "https://login.tailscale.com/admin/acls"
	tailscaleOAuthClientsURL = "https://login.tailscale.com/admin/settings/oauth"

	// policyCopyTarget and policyContinueTarget are the named click targets
	// the integration form drivers assert; only the active screen registers
	// them.
	policyCopyTarget      = "copy-policy"
	policyCopyBlockTarget = "copy-block"
	policyCopyLineTarget  = "copy-line"
	policyContinueTarget  = "continue"
)

// tagOwnersContinueMsg leaves the tagOwners screen for the credential form,
// whether via Continue or the skip key (a tag that already exists needs no
// verification - a working tagged credential is itself the proof).
type tagOwnersContinueMsg struct{}

// policyDiffContinueMsg leaves the diff screen: the user says the Merged
// Policy has been applied, so enable runs again.
type policyDiffContinueMsg struct{}

var tagOwnersSkipKey = WithHelp(NewKeyBinding("s"), "s", "skip - tag exists")

// policyControls is the controls every policy screen ends with: one or more
// visible copy controls (the first sharing its payload and Copied ✓ state
// with ctrl+p) and a Continue button, focused in that order. Tab and the
// arrows move focus, enter presses the focused control, and clicks land on
// the named targets the integration drivers assert.
type policyControls struct {
	copyTargets []string
	focused     int
	copied      []bool
}

func newPolicyControls(copyTargets ...string) policyControls {
	return policyControls{copyTargets: copyTargets, copied: make([]bool, len(copyTargets))}
}

// Update handles the controls' gestures, with handled reporting whether msg
// was one of them - the hosting screen routes anything else (the diff's
// viewport keys, the tagOwners skip) itself. payloads holds one clipboard
// payload per copy control, in copyTargets order.
func (c policyControls) Update(msg tea.Msg, payloads []string, continueMsg tea.Msg) (_ policyControls, _ tea.Cmd, handled bool) {
	count := len(c.copyTargets) + 1
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if key.Matches(msg, tailscaleCopyKey) {
			return c.copy(0, payloads[0])
		}
		switch msg.String() {
		case "tab", "right":
			c.focused = (c.focused + 1) % count
			return c, nil, true
		case "shift+tab", "left":
			c.focused = (c.focused + count - 1) % count
			return c, nil, true
		case "enter":
			if c.focused < len(c.copyTargets) {
				return c.copy(c.focused, payloads[c.focused])
			}
			return c, continueCmd(continueMsg), true
		}

	case MouseEvent:
		if !msg.IsClick {
			return c, nil, true
		}
		if i := slices.Index(c.copyTargets, msg.Target); i >= 0 {
			return c.copy(i, payloads[i])
		}
		if msg.Target == policyContinueTarget {
			return c, continueCmd(continueMsg), true
		}
		return c, nil, true
	}
	return c, nil, false
}

// copyButton and continueButton render the controls with the current focus
// and copied state.
func (c policyControls) copyButton(i int, label string) string {
	return policyCopyControl(c.copyTargets[i], label, c.focused == i, c.copied[i])
}

func (c policyControls) continueButton() string {
	return policyContinueButton(c.focused == len(c.copyTargets))
}

// Private

func (c policyControls) copy(i int, payload string) (policyControls, tea.Cmd, bool) {
	c.copied = slices.Clone(c.copied)
	c.copied[i] = true
	return c, tea.SetClipboard(payload), true
}

// tagOwnersScreen is phase one of the enable flow: the user declares
// tagOwners by hand, before the credential exists, because the admin console
// will not issue a tagged credential until the tag is already in tagOwners.
// Both entry shapes are shown - a whole block, and a single line for a policy
// that already has a tagOwners section - since Once cannot read the policy
// yet to know which applies.
type tagOwnersScreen struct {
	tag      string
	controls policyControls
}

func newTagOwnersScreen(tag string) tagOwnersScreen {
	return tagOwnersScreen{tag: tag, controls: newPolicyControls(policyCopyBlockTarget, policyCopyLineTarget)}
}

func (s tagOwnersScreen) Update(msg tea.Msg) (tagOwnersScreen, tea.Cmd) {
	if msg, ok := msg.(tea.KeyPressMsg); ok && key.Matches(msg, tagOwnersSkipKey) {
		return s, continueCmd(tagOwnersContinueMsg{})
	}
	var cmd tea.Cmd
	s.controls, cmd, _ = s.controls.Update(msg,
		[]string{docker.TagOwnersPolicyBlock(s.tag), docker.TagOwnersPolicyLines(s.tag)}, tagOwnersContinueMsg{})
	return s, cmd
}

func (s tagOwnersScreen) View() string {
	block := docker.TagOwnersPolicyBlock(s.tag)
	line := docker.TagOwnersPolicyLines(s.tag)

	// Each snippet carries its own copy control, so either entry shape is one
	// click away; ctrl+p keeps copying the block, the fresh-policy default.
	steps := lipgloss.JoinVertical(lipgloss.Left,
		policyStepStyle().Render("1. Copy this entry"),
		lipgloss.JoinHorizontal(lipgloss.Center, policySnippetBox(block), " ", s.controls.copyButton(0, "Copy to clipboard")),
		policyMuted("   already have tagOwners? add just this line inside it:"),
		lipgloss.JoinHorizontal(lipgloss.Center, policySnippetBox(line), " ", s.controls.copyButton(1, "Copy to clipboard")),
		"",
		policyStepStyle().Render("2. Paste it")+policyMuted(" in the policy editor: ")+policyLink(tailscalePolicyEditorURL),
		"",
		policyStepStyle().Render("3. Continue")+policyMuted(" - done when "+firstPolicyTag(s.tag)+" is in the tag picker"),
	)

	buttons := lipgloss.PlaceHorizontal(lipgloss.Width(steps), lipgloss.Right, s.controls.continueButton())

	return policyScreenBox().Render(lipgloss.JoinVertical(lipgloss.Center,
		policyScreenTitle("Need tagOwners in Policy File"),
		lipgloss.JoinVertical(lipgloss.Left, steps, "", buttons,
			policyHelpLine("tab focus", "enter press", "ctrl+p copy", "s skip - tag exists", "esc close"))))
}

// policyDiffChrome is every row of the diff screen that is not the viewport:
// box borders (2), title with margin (2), summary (1), the steps block whose
// first row holds the 3-row copy button (5), the 3-row Continue button, and
// the help line with margin (2). TestPolicyDiffScreenFitsSmallTerminals pins
// the count.
const policyDiffChrome = 15

// policyDiffWidth is the diff viewport's width: 72 columns of policy plus
// the scrollbar and the box's chrome measure exactly the 80-column floor.
const policyDiffWidth = 72

// policyDiffScreen shows the Merged Policy as a colour diff against the
// user's own document after a refused enable. The diff exists for
// comprehension; the copy control carries the whole merged document, never
// the diff rendering, because the paste is the critical path.
type policyDiffScreen struct {
	review     docker.PolicyReview
	vp         viewport.Model
	addedLines int
	addedAt    []int // rendered line indices of the additions, for the scrollbar markers
	controls   policyControls
	height     int
}

func newPolicyDiffScreen(review docker.PolicyReview, height int) policyDiffScreen {
	s := policyDiffScreen{review: review, height: height, controls: newPolicyControls(policyCopyTarget)}

	addedStyle := lipgloss.NewStyle().Foreground(Colors.Success)
	var rendered []string
	for _, line := range policyDiffLines(review.Original, review.Merged) {
		text := strings.ReplaceAll(line.text, "\t", "    ")
		// Soft-wrap instead of letting the viewport truncate: a policy line
		// longer than the viewport (hujson formats a whole grant onto one
		// line) must stay readable, especially when it is an addition.
		for row, chunk := range wrapPolicyLine(text, policyDiffWidth-2) {
			prefix := "  "
			if line.added && row == 0 {
				prefix = "+ "
			}
			if line.added {
				s.addedAt = append(s.addedAt, len(rendered))
				rendered = append(rendered, addedStyle.Render(prefix+chunk))
			} else {
				rendered = append(rendered, policyMuted(prefix+chunk))
			}
		}
		if line.added {
			s.addedLines++
		}
	}

	s.vp = viewport.New()
	s.vp.SetContent(strings.Join(rendered, "\n"))
	s.setHeight(height)
	// Open scrolled to the first addition, with a little context above it -
	// on a long policy the changes are usually near the bottom.
	if len(s.addedAt) > 0 {
		s.vp.SetYOffset(max(0, s.addedAt[0]-3))
	}
	return s
}

func (s policyDiffScreen) Update(msg tea.Msg) (policyDiffScreen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.setHeight(msg.Height)
		return s, nil

	case tea.MouseWheelMsg:
		s.vp, _ = s.vp.Update(msg)
		return s, nil
	}

	// The copy carries the whole merged document, never the diff rendering,
	// because the paste is the critical path.
	controls, cmd, handled := s.controls.Update(msg, []string{s.review.Merged}, policyDiffContinueMsg{})
	s.controls = controls
	if handled {
		return s, cmd
	}
	if _, ok := msg.(tea.KeyPressMsg); ok {
		s.vp, _ = s.vp.Update(msg)
	}
	return s, nil
}

func (s policyDiffScreen) View() string {
	summary := lipgloss.NewStyle().Foreground(Colors.Success).Bold(true).
		Render(fmt.Sprintf("+%d lines", s.addedLines)) +
		policyMuted(" · adds "+strings.Join(s.review.Added, ", "))

	steps := lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.JoinHorizontal(lipgloss.Center,
			policyStepStyle().Render("1. Copy the merged file"), "  ",
			s.controls.copyButton(0, "Copy Policy File to Clipboard")),
		policyStepStyle().Render("2. Open the editor")+policyMuted(": ")+policyLink(tailscalePolicyEditorURL)+policyMuted(" - select all"),
		policyStepStyle().Render("3. Paste over the whole file")+policyMuted(", save, then Continue"),
	)

	scroll := fmt.Sprintf("↑/↓ scroll %.0f%%", s.vp.ScrollPercent()*100)
	body := lipgloss.JoinHorizontal(lipgloss.Top, s.vp.View(), " ", s.scrollbar())
	return policyScreenBox().Render(lipgloss.JoinVertical(lipgloss.Center,
		policyScreenTitle("Review the Merged Policy"),
		lipgloss.JoinVertical(lipgloss.Left,
			summary, body, steps,
			lipgloss.PlaceHorizontal(lipgloss.Width(body), lipgloss.Right, s.controls.continueButton()),
			policyHelpLine(scroll, "tab focus", "enter press", "ctrl+p copy", "esc back"))))
}

// Private

func (s *policyDiffScreen) setHeight(height int) {
	s.height = height
	s.vp.SetWidth(policyDiffWidth)
	s.vp.SetHeight(max(3, height-policyDiffChrome))
}

// scrollbar draws a thumb-on-track bar for the diff viewport, or nothing when
// the whole diff already fits. Added lines project onto the track as green
// markers, like an editor's overview ruler, so they are findable from
// anywhere in a long policy.
func (s policyDiffScreen) scrollbar() string {
	total, height := s.vp.TotalLineCount(), s.vp.Height()
	if total <= height {
		return ""
	}
	thumb := max(1, height*height/total)
	pos := int(float64(height-thumb)*s.vp.ScrollPercent() + 0.5)

	markers := make(map[int]bool, len(s.addedAt))
	for _, idx := range s.addedAt {
		markers[min(idx*height/total, height-1)] = true
	}

	track := policyMuted("│")
	thumbCell := lipgloss.NewStyle().Foreground(Colors.Primary).Render("┃")
	markerCell := lipgloss.NewStyle().Foreground(Colors.Success).Render("▐")
	markerThumbCell := lipgloss.NewStyle().Foreground(Colors.Success).Render("┃")
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

// Helpers

type policyDiffLine struct {
	text  string
	added bool
}

// policyDiffLines is a plain LCS line diff. The merge only ever adds, so
// every old line survives and nothing renders as removed; the LCS keeps the
// alignment honest around the insertion points.
func policyDiffLines(oldText, newText string) []policyDiffLine {
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")

	lcs := make([][]int, len(oldLines)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(newLines)+1)
	}
	for i := len(oldLines) - 1; i >= 0; i-- {
		for j := len(newLines) - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}

	var out []policyDiffLine
	i, j := 0, 0
	for i < len(oldLines) && j < len(newLines) {
		switch {
		case oldLines[i] == newLines[j]:
			out = append(out, policyDiffLine{text: oldLines[i]})
			i, j = i+1, j+1
		case lcs[i+1][j] >= lcs[i][j+1]:
			// A dropped old line: cannot happen from an add-only merge, but
			// render it as context rather than lying about a removal.
			out = append(out, policyDiffLine{text: oldLines[i]})
			i++
		default:
			out = append(out, policyDiffLine{text: newLines[j], added: true})
			j++
		}
	}
	for ; j < len(newLines); j++ {
		out = append(out, policyDiffLine{text: newLines[j], added: true})
	}
	for ; i < len(oldLines); i++ {
		out = append(out, policyDiffLine{text: oldLines[i]})
	}
	return out
}

// wrapPolicyLine chunks a line to at most width cells; continuation rows
// carry a two-cell hanging indent so wrapped rows read as one line. Policy
// text is ASCII, so runes measure as cells.
func wrapPolicyLine(text string, width int) []string {
	runes := []rune(text)
	if len(runes) <= width {
		return []string{text}
	}
	rows := []string{string(runes[:width])}
	for rest := runes[width:]; len(rest) > 0; {
		n := min(len(rest), width-2)
		rows = append(rows, "  "+string(rest[:n]))
		rest = rest[n:]
	}
	return rows
}

func continueCmd(msg tea.Msg) tea.Cmd {
	return func() tea.Msg { return msg }
}

// firstPolicyTag names the tag the user should look for in the console's tag
// picker: the first entry of a possibly comma-separated tag setting.
func firstPolicyTag(tag string) string {
	first, _, _ := strings.Cut(tag, ",")
	return strings.TrimSpace(first)
}

func policyCopyControl(target, label string, focused, copied bool) string {
	style := Styles.Focus(Styles.ButtonPrimary, focused)
	if copied {
		style = style.Foreground(Colors.Success)
		// The swap keeps the label's width so the button never moves the row.
		label = lipgloss.PlaceHorizontal(lipgloss.Width(label), lipgloss.Center, "Copied ✓")
	}
	return mouse.Mark(target, style.Render(label))
}

func policyContinueButton(focused bool) string {
	return mouse.Mark(policyContinueTarget, Styles.Focus(Styles.Button, focused).Render("Continue"))
}

// policyScreenBox keeps the tallest screen inside the 80x24 floor the size
// guard enforces: no vertical padding, and two cells horizontally so a
// 72-cell body plus scrollbar still fits 80 columns.
func policyScreenBox() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Colors.Border).
		Padding(0, 2)
}

func policyScreenTitle(s string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(Colors.Primary).MarginBottom(1).Render("Tailscale setup: " + s)
}

func policyStepStyle() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(Colors.Primary)
}

func policyMuted(s string) string {
	return lipgloss.NewStyle().Foreground(Colors.Muted).Render(s)
}

func policyLink(url string) string {
	return lipgloss.NewStyle().Foreground(Colors.Primary).Underline(true).Hyperlink(url).Render(url)
}

func policySnippetBox(s string) string {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(Colors.Primary).
		Padding(0, 2).
		Render(strings.ReplaceAll(s, "\t", "    "))
}

func policyHelpLine(bindings ...string) string {
	return lipgloss.NewStyle().MarginTop(1).Foreground(Colors.Muted).Render(strings.Join(bindings, " · "))
}
