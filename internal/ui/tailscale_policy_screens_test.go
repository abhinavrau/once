package ui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
)

func TestPolicyDiffScreenFitsSmallTerminals(t *testing.T) {
	review := longPolicyReview()
	for _, rows := range []int{24, 46, 60} {
		s := newPolicyDiffScreen(review, rows)
		view := s.View()
		assert.Equal(t, rows, lipgloss.Height(view), "the diff screen must use exactly the %d rows it is given", rows)
		assert.LessOrEqual(t, lipgloss.Width(view), 80, "at %d rows", rows)
		assert.Contains(t, view, "Copy Policy File", "the copy control must stay reachable at %d rows", rows)
		assert.Greater(t, s.vp.TotalLineCount(), s.vp.Height(), "off-screen diff content must be scrollable at %d rows", rows)
	}
}

func TestPolicyDiffScreenOpensScrolledToTheFirstAddition(t *testing.T) {
	s := newPolicyDiffScreen(longPolicyReview(), 24)

	require.NotEmpty(t, s.addedAt)
	assert.Positive(t, s.vp.ScrollPercent(),
		"on a long policy the additions sit near the bottom, so the viewport must open scrolled to them")
	assert.Contains(t, s.vp.View(), "+ ", "the first addition must be on screen")
}

func TestPolicyDiffScreenScrollbarMarksTheAdditions(t *testing.T) {
	s := newPolicyDiffScreen(longPolicyReview(), 24)

	// Scroll away from the additions so the markers stand apart from the
	// thumb (on top of it they render as the thumb in the marker colour).
	s.vp.SetYOffset(0)
	assert.Contains(t, s.scrollbar(), "▐", "added lines must project onto the track as overview-ruler markers")
}

func TestPolicyDiffShowsEveryOriginalLineUnchanged(t *testing.T) {
	review := longPolicyReview()

	nonAdded := 0
	for _, line := range policyDiffLines(review.Original, review.Merged) {
		if !line.added {
			nonAdded++
		}
	}
	assert.Equal(t, len(strings.Split(review.Original, "\n")), nonAdded,
		"the merge only ever adds, so every original line must render as context")
}

func TestPolicyDiffSoftWrapsLongLines(t *testing.T) {
	long := strings.Repeat("x", 200)

	rows := wrapPolicyLine(long, 70)
	require.Greater(t, len(rows), 1, "a line wider than the viewport must wrap, not truncate")
	for _, row := range rows {
		assert.LessOrEqual(t, len(row), 70)
	}
	assert.True(t, strings.HasPrefix(rows[1], "  "), "continuation rows carry a hanging indent")
	assert.Equal(t, long, strings.ReplaceAll(strings.Join(rows, ""), " ", ""), "no content may be lost to wrapping")
}

func TestPolicyDiffCopyTogglesTheCopiedState(t *testing.T) {
	s := newPolicyDiffScreen(longPolicyReview(), 24)

	s, cmd := s.Update(MouseEvent{IsClick: true, Target: policyCopyTarget})
	require.NotNil(t, cmd)
	assert.Equal(t, s.review.Merged, fmt.Sprint(cmd()), "the copy carries the whole merged document, never the diff rendering")
	assert.Contains(t, s.View(), "Copied ✓")
}

func TestTagOwnersScreen_DefaultsToTheGivenTag(t *testing.T) {
	s := newTagOwnersScreen("once")

	assert.Equal(t, "once", s.field.Value())
	view := ansi.Strip(s.View())
	assert.Contains(t, view, "Tag:")
	assert.Contains(t, view, "comma-separated for multiple")
}

func TestTagOwnersScreen_TagFieldLooksEditableAndOpensFocused(t *testing.T) {
	s := newTagOwnersScreen("once")

	assert.Contains(t, ansi.Strip(s.View()), "▏once"+strings.Repeat(" ", tagFieldWidth-len("once"))+"▕",
		"the tag renders in a fixed-width cell with visible edges, the only affordance left once the input box was dropped")
	assert.True(t, s.fieldFocused, "the screen opens with the cursor in the tag field")
	assert.Contains(t, s.View(), ansi.Style{}.ForegroundColor(Colors.Focused).String()+"▏",
		"focus colors the cell edges")

	s, _ = s.Update(keyPressMsg("tab"))
	assert.NotContains(t, s.View(), ansi.Style{}.ForegroundColor(Colors.Focused).String()+"▏",
		"leaving the field returns the edges to the border color")
}

func TestTagOwnersScreen_SnippetsReadAsAlternatives(t *testing.T) {
	view := ansi.Strip(newTagOwnersScreen("once").View())

	assert.Contains(t, view, "OR", "the two snippets are alternatives, not consecutive steps")
}

func TestTagOwnersScreen_ExplainsWhyTheTagIsNeeded(t *testing.T) {
	s := newTagOwnersScreen("once")

	assert.Contains(t, s.View(), "tagOwners before it can issue a tagged credential")
}

func TestTagOwnersScreen_TypingTheTagUpdatesTheSnippetsLive(t *testing.T) {
	s := newTagOwnersScreen("once")
	s, _ = s.Update(MouseEvent{IsClick: true, Target: fieldTarget("tag")})
	for _, r := range "-app" {
		s, _ = s.Update(keyPressMsg(string(r)))
	}

	assert.Equal(t, "once-app", s.field.Value())
	view := normalizeWhitespace(ansi.Strip(s.View()))
	assert.Contains(t, view, normalizeWhitespace(docker.TagOwnersPolicyLines("once-app")),
		"the snippets must follow the field live, normalized the same way enable would")
	assert.NotContains(t, view, `"tag:once":`, "the stale tag must not linger once the field has changed")
}

func TestTagOwnersScreen_BlankTagBlocksContinue(t *testing.T) {
	s := newTagOwnersScreen("once")
	s, _ = s.Update(MouseEvent{IsClick: true, Target: fieldTarget("tag")})
	s.field.SetValue("")
	s, _ = s.Update(keyPressMsg("tab")) // blur the field, focus the first control

	s, cmd := s.Update(keyPressMsg("s"))
	assert.Nil(t, cmd, "the skip key must not proceed with a blank tag")
	assert.Contains(t, s.View(), "Tag is required")
}

// Helpers

// longPolicyReview cans a review whose policy is long enough to scroll at
// every tested size, with the additions near the bottom like a real merge.
func longPolicyReview() docker.PolicyReview {
	var b strings.Builder
	b.WriteString("{\n\t\"hosts\": {\n")
	for i := range 200 {
		fmt.Fprintf(&b, "\t\t\"host-%03d\": \"100.64.0.%d\",\n", i, i%250)
	}
	b.WriteString("\t},\n")
	original := b.String() + "}"
	merged := b.String() +
		"\t\"grants\": [\n\t\t{\"src\": [\"autogroup:member\"], \"dst\": [\"tag:once\"], \"ip\": [\"443\"]},\n\t],\n" +
		"}"
	return docker.PolicyReview{Original: original, Merged: merged, Added: []string{"grants"}}
}
