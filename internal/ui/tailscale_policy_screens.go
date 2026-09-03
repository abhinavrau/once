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
// visible, clickable button next to every snippet.

const (
	tailscalePolicyEditorURL = "https://login.tailscale.com/admin/acls/file"
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
// verification - a working tagged credential is itself the proof). tag is
// the field's current (bare, comma-separated) value, carried to the
// credential form and the eventual enable submission.
type tagOwnersContinueMsg struct{ tag string }

// policyDiffContinueMsg leaves the diff screen: the user says the Merged
// Policy has been applied, so enable runs again.
type policyDiffContinueMsg struct{}

var tagOwnersSkipKey = WithHelp(NewKeyBinding("s"), "s", "skip - tag exists")

// policyControls is the controls every policy screen ends with: one or more
// visible copy controls and a Continue button, focused in that order. Tab and the
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
// yet to know which applies. The tag itself is edited here (live: the
// snippets and the credential form's eventual submission all read the same
// field), not on the credential screen - the tagOwners entry the user pastes
// and the tag the credential ends up scoped to can never drift apart.
// tagFieldWidth is the editable width of the tag cell: wide enough to show a
// couple of comma-separated tags without pushing the row past the blurb.
const tagFieldWidth = 28

type tagOwnersScreen struct {
	field        *TextField
	fieldFocused bool
	err          string
	controls     policyControls
	height       int
}

// tagOwnersRoomyFloor is the terminal height at which the screen can afford
// its two breathing rows (below the tag field's blurb, and below step 2).
// Under it the content is what has to fit, not the spacing.
const tagOwnersRoomyFloor = 26

func newTagOwnersScreen(tag string) tagOwnersScreen {
	field := NewTextField("once")
	field.SetWidth(tagFieldWidth)
	field.SetValue(tag)
	s := tagOwnersScreen{field: field, controls: newPolicyControls(policyCopyBlockTarget, policyCopyLineTarget)}
	// The tag is the one thing on this screen the user may need to change, so
	// the screen opens with the cursor in it; Init carries the blink command.
	s, _ = s.focusField()
	return s
}

// Init returns the cursor command for the field the screen opens focused on.
func (s tagOwnersScreen) Init() tea.Cmd {
	return s.field.Focus()
}

func (s tagOwnersScreen) Update(msg tea.Msg) (tagOwnersScreen, tea.Cmd) {
	if msg, ok := msg.(tea.WindowSizeMsg); ok {
		s.height = msg.Height
		return s, nil
	}

	tag := s.field.Value()
	payloads := []string{docker.TagOwnersPolicyBlock(tag), docker.TagOwnersPolicyLines(tag)}

	if click, ok := msg.(MouseEvent); ok && click.IsClick && click.Target == fieldTarget("tag") {
		return s.focusField()
	}

	if s.fieldFocused {
		if msg, ok := msg.(tea.KeyPressMsg); ok && (msg.String() == "tab" || msg.String() == "enter") {
			s.fieldFocused = false
			s.field.Blur()
			return s, nil
		}
		if click, ok := msg.(MouseEvent); !ok || !click.IsClick {
			s.err = ""
			cmd := s.field.Update(msg)
			return s, cmd
		}
		// A click elsewhere on the screen moves focus off the field; fall
		// through so the same click still reaches its own target below.
		s.fieldFocused = false
		s.field.Blur()
	}

	if msg, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(msg, tagOwnersSkipKey):
			return s.trySkip()
		case msg.String() == "shift+tab" && s.controls.focused == 0:
			return s.focusField()
		}
	}
	if s.wantsBlankContinue(msg, tag) {
		s.err = "Tag is required"
		return s, nil
	}

	var cmd tea.Cmd
	s.controls, cmd, _ = s.controls.Update(msg, payloads, tagOwnersContinueMsg{tag: tag})
	return s, cmd
}

func (s tagOwnersScreen) View() string {
	tag := s.field.Value()
	block := docker.TagOwnersPolicyBlock(tag)
	line := docker.TagOwnersPolicyLines(tag)

	tagRow := lipgloss.JoinHorizontal(lipgloss.Center,
		policyStepStyle().Render("Tag:"), " ",
		mouse.Mark(fieldTarget("tag"), s.fieldCell()),
		policyMuted(" (comma-separated for multiple)"))

	var top []string
	top = append(top, tagRow)
	if s.err != "" {
		top = append(top, lipgloss.NewStyle().Foreground(Colors.Error).Render(s.err))
	}
	top = append(top,
		lipgloss.NewStyle().Foreground(Colors.Muted).Width(74).
			Render("Once needs this tag in tagOwners before it can issue a tagged credential."))
	top = append(top, s.spacer()...)

	// Each snippet carries its own copy control, so either entry shape is one
	// click away.
	top = append(top,
		policyStepStyle().Render("1. Copy this entry"),
		lipgloss.JoinHorizontal(lipgloss.Center, policySnippetBox(block), " ", s.controls.copyButton(0, "Copy to clipboard")),
		// The two snippets are alternatives, not steps: only the OR between
		// them says so.
		policyStepStyle().Render("   OR"),
		policyMuted("   already have tagOwners? add just this line inside it:"),
		lipgloss.JoinHorizontal(lipgloss.Center, policySnippetBox(line), " ", s.controls.copyButton(1, "Copy to clipboard")),
		policyStepStyle().Render("2. Paste it")+policyMuted(" in the editor: ")+linkedURL(tailscalePolicyEditorURL),
	)
	top = append(top, s.spacer()...)
	top = append(top,
		policyStepStyle().Render("3. Continue")+policyMuted(" - done when "+firstPolicyTag(prefixedTag(tag))+" is in the tag picker"),
	)

	steps := lipgloss.JoinVertical(lipgloss.Left, top...)
	buttons := lipgloss.PlaceHorizontal(lipgloss.Width(steps), lipgloss.Right, s.controls.continueButton())

	return policyScreenBox().Render(lipgloss.JoinVertical(lipgloss.Center,
		policyScreenTitle("Need tagOwners in Policy File"),
		lipgloss.JoinVertical(lipgloss.Left, steps, "", buttons)))
}

// Private

// spacer is one blank row, or none on a terminal too short to spend rows on
// spacing.
func (s tagOwnersScreen) spacer() []string {
	if s.height != 0 && s.height < tagOwnersRoomyFloor {
		return nil
	}
	return []string{""}
}

// fieldCell renders the tag input. The screen's 24-row floor leaves no room
// for the usual bordered Styles.Input box, so the field reads as editable
// from a tinted fixed-width cell between edge glyphs instead - both survive
// terminals that render no background tint, and neither costs a row.
func (s tagOwnersScreen) fieldCell() string {
	edge := lipgloss.NewStyle().Foreground(Colors.Border)
	if s.fieldFocused {
		edge = edge.Foreground(Colors.Focused)
	}
	cell := lipgloss.NewStyle().Width(tagFieldWidth).Render(s.field.View())
	return WithBackground(Colors.BackgroundTint, edge.Render("▏")+cell+edge.Render("▕"))
}

// focusField moves focus onto the tag field, from a click or a shift+tab off
// the first control.
func (s tagOwnersScreen) focusField() (tagOwnersScreen, tea.Cmd) {
	s.controls.focused = 0
	s.fieldFocused = true
	return s, s.field.Focus()
}

// trySkip is the skip key: a tag that already exists needs no verification,
// but there must be one to check for.
func (s tagOwnersScreen) trySkip() (tagOwnersScreen, tea.Cmd) {
	if strings.TrimSpace(s.field.Value()) == "" {
		s.err = "Tag is required"
		return s, nil
	}
	return s, continueCmd(tagOwnersContinueMsg{tag: s.field.Value()})
}

// wantsBlankContinue reports whether msg would otherwise move on (Continue's
// enter or click) with tag empty - the one gesture this screen blocks.
func (s tagOwnersScreen) wantsBlankContinue(msg tea.Msg, tag string) bool {
	if strings.TrimSpace(tag) != "" {
		return false
	}
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return msg.String() == "enter" && s.controls.focused == len(s.controls.copyTargets)
	case MouseEvent:
		return msg.IsClick && msg.Target == policyContinueTarget
	}
	return false
}

// policyDiffChrome is every row of the diff screen that is not the viewport:
// box borders (2), title with margin (2), summary (1), the steps block whose
// first row holds the 3-row copy button (5), and the 3-row Continue button.
// TestPolicyDiffScreenFitsSmallTerminals pins the count.
const policyDiffChrome = 13

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
		policyStepStyle().Render("2. Open")+policyMuted(" ")+linkedURL(tailscalePolicyEditorURL)+policyMuted(" - select all"),
		policyStepStyle().Render("3. Paste over the whole file")+policyMuted(", save, then Continue"),
	)

	body := lipgloss.JoinHorizontal(lipgloss.Top, s.vp.View(), " ", s.scrollbar())
	return policyScreenBox().Render(lipgloss.JoinVertical(lipgloss.Center,
		policyScreenTitle("Review the Merged Policy"),
		lipgloss.JoinVertical(lipgloss.Left,
			summary, body, steps,
			lipgloss.PlaceHorizontal(lipgloss.Width(body), lipgloss.Right, s.controls.continueButton()))))
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

func policySnippetBox(s string) string {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(Colors.Primary).
		Padding(0, 2).
		Render(strings.ReplaceAll(s, "\t", "    "))
}

// stripTagPrefix drops a leading "tag:" from each comma-separated segment -
// the bare form the tagOwners field edits and displays. docker's normalizeTag
// (run wherever the value is actually consumed: the policy snippets, the
// enable submission) re-adds the prefix, so it tolerates either form.
func stripTagPrefix(tag string) string {
	segments := strings.Split(tag, ",")
	for i, seg := range segments {
		segments[i] = strings.TrimPrefix(strings.TrimSpace(seg), "tag:")
	}
	return strings.Join(segments, ",")
}

// prefixedTag is stripTagPrefix's inverse, for the one place the field's bare
// value needs to be shown the way the console actually displays it (the
// tagOwners screen's "done when tag:x is in the tag picker" line).
func prefixedTag(tag string) string {
	segments := strings.Split(tag, ",")
	for i, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg != "" && !strings.HasPrefix(seg, "tag:") {
			seg = "tag:" + seg
		}
		segments[i] = seg
	}
	return strings.Join(segments, ",")
}

// tagOwnersHelpText is the tagOwners screen's F1 popup body.
const tagOwnersHelpText = `The Tailscale admin console will not issue an OAuth client tagged with your tag until that tag is declared in the tailnet policy's tagOwners section.

Edit the tag above, then copy whichever entry matches your policy: the whole block for a policy with no tagOwners section yet, or just the added line for one that already has it. Paste it into the policy editor, then Continue.

The screen opens with the cursor in the tag field; tab leaves it, and from there s skips this screen for a tag that is already declared.
`

// policyDiffHelpText is the Merged Policy diff screen's F1 popup body.
const policyDiffHelpText = `Once tried to enable Tailscale, but the tailnet policy was missing sections it needs. This is the Merged Policy: your own document with exactly those sections added, highlighted in green.

Copy the whole file, paste it over your policy in the editor, save it, then Continue to retry with the same settings.
`
