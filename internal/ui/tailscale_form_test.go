package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
)

func TestTailscaleForm_EnableFormDefaultsToNodesWithNoModeField(t *testing.T) {
	m := advanceToCredential(t, NewTailscaleForm(newTestNamespace()))

	assert.Equal(t, 4, m.form.ItemCount(), "no Mode field: nodes has no tailscaled of its own to choose a mode for")
	assert.Equal(t, "tag:once", m.form.TextField("tag").Value())
	assert.Equal(t, 0, m.form.RadioField("topology").Selected(), "nodes should be preselected on a fresh enable")
	assert.Equal(t, "topology", m.form.items[0].Key, "topology leads: the guidance's step 1 selects it")

	view := m.View()
	assert.Contains(t, view, "Client ID")
	assert.Contains(t, view, "Client Secret")
	assert.Contains(t, view, "Topology")
	assert.Contains(t, view, "(•) Nodes")
	assert.NotContains(t, view, "Mode")
	assert.Contains(t, view, "Continue")
	assert.NotContains(t, view, "Cancel", "esc covers cancelling; a Cancel button would be a second control for the same exit")
	assert.Contains(t, view, "copy policy snippet")

	nodesFacts, err := docker.TailscaleTopologyFacts(docker.TailnetTopologyNodes)
	require.NoError(t, err)
	assert.Contains(t, normalizeWhitespace(ansi.Strip(m.setupView(true, false))),
		normalizeWhitespace(strings.Join(nodesFacts.OAuthScopes, ", ")),
		"the scope list must come from the topology's facts")
}

func TestTailscaleForm_FreshEnableOpensOnTheTagOwnersScreen(t *testing.T) {
	m := NewTailscaleForm(newTestNamespace())

	view := ansi.Strip(m.View())
	assert.Contains(t, view, "Need tagOwners in Policy File")
	assert.Contains(t, normalizeWhitespace(view), normalizeWhitespace(docker.TagOwnersPolicyLines("tag:once")),
		"both entry shapes must show the real tagOwners entry")
	assert.NotContains(t, view, "Client ID", "the credential form comes after the tagOwners declaration")

	// The skip key: a tag that already exists needs no verification.
	c, cmd := m.Update(keyPressMsg("s"))
	require.NotNil(t, cmd)
	c, _ = c.(TailscaleForm).Update(cmd())
	assert.Contains(t, c.(TailscaleForm).View(), "Client ID")

	// Esc from the credential form returns to the tagOwners screen.
	c, _ = c.(TailscaleForm).Update(keyPressMsg("esc"))
	assert.Contains(t, ansi.Strip(c.(TailscaleForm).View()), "Need tagOwners in Policy File")
}

func TestTailscaleForm_TagOwnersCopyControlSharesOnePayloadWithCtrlP(t *testing.T) {
	m := NewTailscaleForm(newTestNamespace())

	c, cmd := m.Update(MouseEvent{IsClick: true, Target: policyCopyTarget})
	require.NotNil(t, cmd)
	assert.Equal(t, docker.TagOwnersPolicyBlock("tag:once"), fmt.Sprint(cmd()))
	assert.Contains(t, c.(TailscaleForm).View(), "Copied ✓")

	_, cmd = m.Update(keyPressMsg("ctrl+p"))
	require.NotNil(t, cmd)
	assert.Equal(t, docker.TagOwnersPolicyBlock("tag:once"), fmt.Sprint(cmd()), "ctrl+p and the visible control share one copy path")
}

func TestTailscaleForm_SelectingServicesRevealsTheModeField(t *testing.T) {
	m := advanceToCredential(t, NewTailscaleForm(newTestNamespace()))

	m = clickField(m, "topology") // nodes -> services

	assert.Equal(t, 1, m.form.RadioField("topology").Selected())
	assert.Equal(t, 5, m.form.ItemCount(), "services offers a Mode choice")
	assert.Equal(t, 0, m.form.RadioField("mode").Selected(), "sidecar should be preselected (ADR-0007)")

	view := m.View()
	assert.Contains(t, view, "(•) Services")
	assert.Contains(t, view, "(•) Sidecar")

	servicesFacts, err := docker.TailscaleTopologyFacts(docker.TailnetTopologyServices)
	require.NoError(t, err)
	assert.Contains(t, normalizeWhitespace(ansi.Strip(m.setupView(true, false))),
		normalizeWhitespace(strings.Join(servicesFacts.OAuthScopes, ", ")),
		"the scope list must follow the topology selection live")

	m = clickField(m, "topology") // services -> nodes
	assert.Equal(t, 4, m.form.ItemCount(), "the Mode field disappears again")
}

func TestTailscaleForm_CtrlPCopiesTheSelectedTopologysSnippet(t *testing.T) {
	m := advanceToCredential(t, NewTailscaleForm(newTestNamespace()))
	m.form.TextField("tag").SetValue("tag:once")

	_, cmd := m.Update(keyPressMsg("ctrl+p"))
	require.NotNil(t, cmd)
	assert.Equal(t, docker.NodesPolicySnippet("tag:once"), fmt.Sprint(cmd()))

	m = clickField(m, "topology")
	_, cmd = m.Update(keyPressMsg("ctrl+p"))
	require.NotNil(t, cmd)
	assert.Equal(t, docker.PolicySnippet("tag:once"), fmt.Sprint(cmd()))
}

func TestTailscaleForm_ARefusedEnableShowsTheMergedPolicyDiff(t *testing.T) {
	m := advanceToCredential(t, NewTailscaleForm(newTestNamespace()))
	m.running = true
	review := docker.PolicyReview{
		Original: "{\n\t\"acls\": [],\n}",
		Merged:   "{\n\t\"acls\": [],\n\t\"grants\": [\n\t\t{\"src\": [\"autogroup:member\"]},\n\t],\n}",
		Added:    []string{"grants"},
	}

	c, _ := m.Update(tailscaleFormFinishedMsg{err: &docker.PolicyRefusedError{Review: review}})
	got := c.(TailscaleForm)

	view := ansi.Strip(got.View())
	assert.Contains(t, view, "Review the Merged Policy")
	assert.Contains(t, view, "+3 lines")
	assert.Contains(t, view, "adds grants")
	assert.Contains(t, view, "Copy Policy File")

	// ctrl+p and the visible control both carry the whole merged document.
	_, cmd := got.Update(keyPressMsg("ctrl+p"))
	require.NotNil(t, cmd)
	assert.Equal(t, review.Merged, fmt.Sprint(cmd()), "the fix for a refusal is the Merged Policy, not the static snippet")
	_, cmd = got.Update(MouseEvent{IsClick: true, Target: policyCopyTarget})
	require.NotNil(t, cmd)
	assert.Equal(t, review.Merged, fmt.Sprint(cmd()))

	// Esc returns to the credential form, where the refusal message waits.
	c, _ = got.Update(keyPressMsg("esc"))
	formView := normalizeWhitespace(ansi.Strip(c.(TailscaleForm).View()))
	assert.Contains(t, formView, "nothing was installed")
	assert.Contains(t, formView, "ctrl+p copies the Merged Policy")
}

func TestTailscaleForm_DiffContinueRerunsTheEnable(t *testing.T) {
	m := advanceToCredential(t, NewTailscaleForm(newTestNamespace()))
	c, _ := m.Update(tailscaleEnableSubmitMsg{id: "id", secret: "secret", tag: "tag:once", topology: docker.TailnetTopologyNodes})
	m = c.(TailscaleForm)
	require.True(t, m.running)

	review := docker.PolicyReview{Original: "{\n}", Merged: "{\n\t\"grants\": [],\n}", Added: []string{"grants"}}
	c, _ = m.Update(tailscaleFormFinishedMsg{err: &docker.PolicyRefusedError{Review: review}})
	m = c.(TailscaleForm)
	require.False(t, m.running)

	c, cmd := m.Update(MouseEvent{IsClick: true, Target: policyContinueTarget})
	require.NotNil(t, cmd)
	c, _ = c.(TailscaleForm).Update(cmd())
	got := c.(TailscaleForm)
	assert.True(t, got.running, "the diff's Continue means the policy was applied, so the same enable runs again")
	assert.Equal(t, "id", got.lastEnable.id)
}

func TestTailscaleForm_StaleStopAfterDiffContinueShowsTheError(t *testing.T) {
	m := rerunEnableFromDiff(t)

	c, _ := m.Update(tailscaleFormFinishedMsg{err: docker.ErrPolicyChangedDuringEnable})

	got := c.(TailscaleForm)
	assert.Equal(t, tailscaleScreenCredential, got.screen, "a non-refusal outcome must leave the stale diff, or the user never sees it")
	view := normalizeWhitespace(ansi.Strip(got.View()))
	assert.Contains(t, view, "edited while enable was running")
	assert.NotContains(t, view, "Review the Merged Policy")
}

func TestTailscaleForm_WarningsAfterDiffContinueShowOnTheForm(t *testing.T) {
	m := rerunEnableFromDiff(t)

	c, _ := m.Update(tailscaleFormFinishedMsg{warnings: []string{"only Tailscale Funnel breaks"}})

	got := c.(TailscaleForm)
	assert.Equal(t, tailscaleScreenCredential, got.screen)
	view := normalizeWhitespace(ansi.Strip(got.View()))
	assert.Contains(t, view, "only Tailscale Funnel breaks", "warn-and-proceed must be visible after the diff's Continue")
	assert.NotContains(t, view, "Review the Merged Policy")
}

func TestTailscaleForm_EnabledFormShowsStoredSettingsWithBothActions(t *testing.T) {
	m := newTailscaleForm(newTestNamespace(), true, docker.TailscaleSettings{
		ClientID:     "K123456CNTRL",
		ClientSecret: "tskey-client-secret",
		Tag:          "tag:custom",
		Mode:         docker.TailscaleModeHost,
		Topology:     docker.TailnetTopologyServices,
	}, nil)

	assert.Equal(t, "K123456CNTRL", m.form.TextField("client-id").Value())
	assert.Equal(t, "tskey-client-secret", m.form.TextField("client-secret").Value())
	assert.Equal(t, "tag:custom", m.form.TextField("tag").Value())
	assert.Equal(t, 1, m.form.RadioField("topology").Selected(), "stored services topology should preselect Services")
	assert.Equal(t, 1, m.form.RadioField("mode").Selected(), "stored host mode should preselect Host")

	view := m.View()
	assert.Contains(t, view, "Tailscale is enabled")
	assert.Contains(t, view, "(•) Services")
	assert.Contains(t, view, "(•) Host")
	assert.Contains(t, view, "Enable")
	assert.Contains(t, view, "Disable")
	assert.NotContains(t, view, "tskey-client-secret", "the stored secret must stay masked")
}

func TestTailscaleForm_EnabledFormWithStoredNodesTopologyHasNoModeField(t *testing.T) {
	m := newTailscaleForm(newTestNamespace(), true, docker.TailscaleSettings{
		Topology: docker.TailnetTopologyNodes,
	}, nil)

	assert.Equal(t, 0, m.form.RadioField("topology").Selected())
	assert.Equal(t, 4, m.form.ItemCount())
}

func TestTailscaleForm_OlderSetupShowsTheFacadeMessageVerbatim(t *testing.T) {
	m := newTailscaleForm(newTestNamespace(), true, docker.TailscaleSettings{}, docker.ErrOlderOnceSetup)

	// The message word-wraps at the form's text width, so compare with
	// whitespace normalized rather than asserting a literal substring.
	assert.Contains(t, normalizeWhitespace(ansi.Strip(m.setupView(true, false))), normalizeWhitespace(docker.ErrOlderOnceSetup.Error()))
	// The install is still enabled (a provider container exists), so the
	// escape hatch (re-run enable) and Disable both stay reachable.
	assert.Contains(t, m.View(), "Disable")
}

func TestTailscaleForm_EnabledFormSubmitReenablesWithEditedMode(t *testing.T) {
	m := newTailscaleForm(newTestNamespace(), true, docker.TailscaleSettings{
		ClientID:     "id",
		ClientSecret: "secret",
		Tag:          "tag:once",
		Mode:         docker.TailscaleModeSidecar,
		Topology:     docker.TailnetTopologyServices,
	}, nil)
	m.form.RadioField("mode").SetSelected(1)

	msg := submitAfterTabs(t, m.form, m.form.ItemCount())
	assert.Equal(t, tailscaleEnableSubmitMsg{id: "id", secret: "secret", tag: "tag:once", mode: docker.TailscaleModeHost, topology: docker.TailnetTopologyServices}, msg)
}

func TestTailscaleForm_EnabledFormDisableActionWorksWithBlankStoredSettings(t *testing.T) {
	// An enabled install's stored credentials may be blank; the fields must not
	// be required then, or neither action could ever submit.
	m := newTailscaleForm(newTestNamespace(), true, docker.TailscaleSettings{Topology: docker.TailnetTopologyServices, Mode: docker.TailscaleModeSidecar}, nil)

	msg := submitAfterTabs(t, m.form, m.form.ItemCount()+1) // fields, Enable, → Disable action
	assert.IsType(t, tailscaleDisableSubmitMsg{}, msg)
}

func TestTailscaleForm_SubmitCarriesSelectedMode(t *testing.T) {
	submitMode := func(selected int) string {
		m := advanceToCredential(t, NewTailscaleForm(newTestNamespace()))
		m = clickField(m, "topology") // reveal the Mode field under services; moves focus there
		m.form.TextField("client-id").SetValue("id")
		m.form.TextField("client-secret").SetValue("secret")
		m.form.RadioField("mode").SetSelected(selected)

		// Click submit directly, rather than tabbing a fixed count from
		// focus's now-uncertain position (the topology click above moved
		// it off item 0).
		_, cmd := m.form.Update(MouseEvent{IsClick: true, Target: "submit"})
		require.NotNil(t, cmd)
		msg, ok := cmd().(tailscaleEnableSubmitMsg)
		require.True(t, ok)
		return msg.mode
	}

	assert.Equal(t, docker.TailscaleModeSidecar, submitMode(0))
	assert.Equal(t, docker.TailscaleModeHost, submitMode(1))
}

func TestTailscaleForm_SubmitCarriesSelectedTopology(t *testing.T) {
	m := NewTailscaleForm(newTestNamespace())
	m.form.TextField("client-id").SetValue("id")
	m.form.TextField("client-secret").SetValue("secret")

	msg, ok := submitAfterTabs(t, m.form, m.form.ItemCount()).(tailscaleEnableSubmitMsg)
	require.True(t, ok)
	assert.Equal(t, docker.TailnetTopologyNodes, msg.topology)
}

func TestTailscaleForm_EnableSubmitRunsInBackground(t *testing.T) {
	m := NewTailscaleForm(newTestNamespace())

	c, cmd := m.Update(tailscaleEnableSubmitMsg{id: "id", secret: "secret", tag: "tag:once"})

	got := c.(TailscaleForm)
	assert.True(t, got.running)
	assert.NotNil(t, cmd)
}

func TestTailscaleForm_StatusShowsWhileRunningAndKeepsListening(t *testing.T) {
	m := NewTailscaleForm(newTestNamespace())
	m.running = true
	m.statusChan = make(chan string, 1)

	c, cmd := m.Update(tailscaleStatusMsg("waiting up to 1m30s for the tailnet to approve: whoami"))

	got := c.(TailscaleForm)
	assert.Contains(t, got.View(), "waiting up to 1m30s")
	require.NotNil(t, cmd, "the form must keep listening for the next status")

	got.statusChan <- "next status"
	assert.Equal(t, tailscaleStatusMsg("next status"), cmd())

	// The channel closing (task finished) ends the relay without a message.
	close(got.statusChan)
	_, cmd = got.Update(tailscaleStatusMsg("late status"))
	assert.Nil(t, cmd())
}

func TestTailscaleForm_CopyKeyCopiesSnippetForEnteredTag(t *testing.T) {
	m := advanceToCredential(t, NewTailscaleForm(newTestNamespace()))

	_, cmd := m.Update(keyPressMsg("ctrl+p"))
	assert.NotNil(t, cmd)
}

func TestTailscaleForm_FinishedErrorStaysOpen(t *testing.T) {
	m := advanceToCredential(t, NewTailscaleForm(newTestNamespace()))
	m.running = true

	c, _ := m.Update(tailscaleFormFinishedMsg{err: errors.New("probe failed")})

	got := c.(TailscaleForm)
	require.Error(t, got.err)
	assert.False(t, got.running)
	assert.Contains(t, got.View(), "probe failed")
}

func TestTailscaleForm_TagPolicyErrorPointsAtSnippet(t *testing.T) {
	m := advanceToCredential(t, NewTailscaleForm(newTestNamespace()))
	m.running = true

	c, _ := m.Update(tailscaleFormFinishedMsg{err: fmt.Errorf("probe: %w", docker.ErrTagNotInPolicy)})

	got := c.(TailscaleForm)
	assert.Contains(t, got.View(), "ctrl+p copies the required policy snippet")
}

func TestTailscaleForm_FinishedSuccessCloses(t *testing.T) {
	m := NewTailscaleForm(newTestNamespace())
	m.running = true

	_, cmd := m.Update(tailscaleFormFinishedMsg{})

	require.NotNil(t, cmd)
	assert.IsType(t, TailscaleFormCloseMsg{}, cmd())
}

// Helpers

// advanceToCredential steps a fresh form past the tagOwners screen, the way
// Continue or the skip key would.
func advanceToCredential(t *testing.T, m TailscaleForm) TailscaleForm {
	t.Helper()
	c, _ := m.Update(tagOwnersContinueMsg{})
	return c.(TailscaleForm)
}

// rerunEnableFromDiff drives a form to the Merged Policy diff via a refused
// enable and presses its Continue, leaving the re-run enable in flight -
// the state every post-Continue outcome test starts from.
func rerunEnableFromDiff(t *testing.T) TailscaleForm {
	t.Helper()
	m := advanceToCredential(t, NewTailscaleForm(newTestNamespace()))
	c, _ := m.Update(tailscaleEnableSubmitMsg{id: "id", secret: "secret", tag: "tag:once", topology: docker.TailnetTopologyNodes})

	review := docker.PolicyReview{Original: "{\n}", Merged: "{\n\t\"grants\": [],\n}", Added: []string{"grants"}}
	c, _ = c.(TailscaleForm).Update(tailscaleFormFinishedMsg{err: &docker.PolicyRefusedError{Review: review}})

	c, cmd := c.(TailscaleForm).Update(MouseEvent{IsClick: true, Target: policyContinueTarget})
	require.NotNil(t, cmd)
	c, _ = c.(TailscaleForm).Update(cmd())
	m = c.(TailscaleForm)
	require.True(t, m.running)
	require.Equal(t, tailscaleScreenDiff, m.screen, "the diff stays up while the re-run enable is in flight")
	return m
}

// normalizeWhitespace collapses runs of whitespace to a single space, so a
// multi-line fact compares against rendered content regardless of the
// line-width padding or word-wrapping lipgloss adds around it.
func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// clickField sends a click at the named field's mouse target, exactly as the
// integration UI driver does, and returns the updated form.
func clickField(m TailscaleForm, key string) TailscaleForm {
	c, _ := m.Update(MouseEvent{IsClick: true, Target: fieldTarget(key)})
	return c.(TailscaleForm)
}

// submitAfterTabs tabs through the form and presses enter, returning the
// message the landed-on button emits.
func submitAfterTabs(t *testing.T, f Form, tabs int) tea.Msg {
	t.Helper()
	for range tabs {
		f, _ = f.Update(keyPressMsg("tab"))
	}
	_, cmd := f.Update(keyPressMsg("enter"))
	require.NotNil(t, cmd)
	return cmd()
}

func TestTailscaleForm_TagOwnersScreenFitsThe24RowFloor(t *testing.T) {
	m := NewTailscaleForm(newTestNamespace())

	view := m.View()
	assert.LessOrEqual(t, lipgloss.Height(view), 24)
	assert.LessOrEqual(t, lipgloss.Width(view), 80)
}

func TestTailscaleForm_CredentialScreenFitsThe24RowFloor(t *testing.T) {
	for _, rows := range []int{24, 46, 60} {
		// A fresh form per size: the field state lives behind pointers, so a
		// shared form would carry one iteration's topology click into the next.
		m := advanceToCredential(t, NewTailscaleForm(newTestNamespace()))
		c, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: rows})
		sized := c.(TailscaleForm)
		view := sized.View()
		assert.LessOrEqual(t, lipgloss.Height(view), rows, "at %d rows", rows)
		assert.LessOrEqual(t, lipgloss.Width(view), 80, "at %d rows", rows)
		assert.Contains(t, view, "Client ID", "the fields must stay reachable at %d rows", rows)
		assert.Contains(t, view, "Continue", "at %d rows", rows)

		services := clickField(sized, "topology") // reveals the Mode field
		view = services.View()
		assert.LessOrEqual(t, lipgloss.Height(view), rows, "at %d rows with the Mode field", rows)
		assert.Contains(t, ansi.Strip(view), "(•) Sidecar", "at %d rows", rows)
	}
}

func TestTailscaleForm_GuidanceCollapsesBeforeTheFields(t *testing.T) {
	m := advanceToCredential(t, NewTailscaleForm(newTestNamespace()))

	c, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 60})
	roomy := ansi.Strip(c.(TailscaleForm).View())
	assert.Contains(t, roomy, "Create a new OAuth client", "the guidance shows when there is room")

	c, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	floor := ansi.Strip(c.(TailscaleForm).View())
	assert.NotContains(t, floor, "Create a new OAuth client", "the guidance collapses at the floor")
	assert.Contains(t, floor, "Client ID", "the fields survive the collapse")
}

func TestTailscaleForm_TopologyClickKeepsTheGuidanceVisible(t *testing.T) {
	m := advanceToCredential(t, NewTailscaleForm(newTestNamespace()))
	c, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 45})
	nodes := c.(TailscaleForm)
	require.Contains(t, ansi.Strip(nodes.View()), "Create a new OAuth client")

	services := clickField(nodes, "topology") // reveals the Mode field
	view := services.View()
	assert.Contains(t, ansi.Strip(view), "Create a new OAuth client", "selecting a topology must not hide the steps that explain it")
	assert.LessOrEqual(t, lipgloss.Height(view), 45)
}
