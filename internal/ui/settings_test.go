package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
)

func TestSettings_InitialStateIsForm(t *testing.T) {
	s := testSettings()
	assert.Equal(t, settingsStateForm, s.state)
}

func TestSettings_EscNavigatesToDashboard(t *testing.T) {
	s := testSettings()
	s, _ = updateSettings(s, tea.WindowSizeMsg{Width: 80, Height: 24})

	_, cmd := updateSettings(s, keyPressMsg("esc"))
	require.NotNil(t, cmd)

	msg := cmd()
	navMsg, ok := msg.(NavigateToDashboardMsg)
	require.True(t, ok, "expected NavigateToDashboardMsg, got %T", msg)
	assert.Equal(t, "test-app", navMsg.AppName)
}

func TestSettings_CancelNavigatesToDashboard(t *testing.T) {
	s := testSettings()
	_, cmd := updateSettings(s, SettingsSectionCancelMsg{})
	require.NotNil(t, cmd)

	msg := cmd()
	_, ok := msg.(NavigateToDashboardMsg)
	assert.True(t, ok)
}

func TestSettings_SubmitStartsApply(t *testing.T) {
	s := testSettings()
	s, _ = updateSettings(s, tea.WindowSizeMsg{Width: 80, Height: 24})

	changed := s.app.Settings
	changed.Host = "new.example.com"

	// The spine owns the ordering (validation, host conflict, no-op
	// short-circuit), so every submit hands off to it.
	s, _ = updateSettings(s, SettingsSectionSubmitMsg{Settings: changed})
	assert.Equal(t, settingsStateDeploying, s.state)
}

func TestSettings_DeployFinishedNavigatesToApp(t *testing.T) {
	s := testSettings()
	s.state = settingsStateDeploying

	_, cmd := updateSettings(s, settingsDeployFinishedMsg{})
	require.NotNil(t, cmd)

	msg := cmd()
	_, ok := msg.(NavigateToAppMsg)
	assert.True(t, ok)
}

func TestSettings_DeployFinishedWithErrorSurfacesError(t *testing.T) {
	s := testSettings()

	changed := s.app.Settings
	changed.Host = "new.example.com"
	s, _ = updateSettings(s, SettingsSectionSubmitMsg{Settings: changed})
	require.Equal(t, settingsStateDeploying, s.state)

	s, cmd := updateSettings(s, settingsDeployFinishedMsg{err: assert.AnError})
	assert.Nil(t, cmd)
	assert.Equal(t, settingsStateForm, s.state)
	assert.Equal(t, assert.AnError, s.err)
}

func TestSettings_DeployFinishedWithWarningsKeepsFormOpen(t *testing.T) {
	s := testSettings()
	s.width = 80
	s.height = 24
	s.state = settingsStateDeploying

	s, cmd := updateSettings(s, settingsDeployFinishedMsg{warnings: []string{"could not resolve app.example.com"}})
	assert.Nil(t, cmd)
	assert.Equal(t, settingsStateForm, s.state)
	assert.Contains(t, s.View(), "could not resolve app.example.com")

	s, _ = updateSettings(s, keyPressMsg("a"))
	assert.Empty(t, s.warnings)
}

func TestSettings_ActionFinishedWithError(t *testing.T) {
	s := testSettings()
	s.state = settingsStateRunningAction

	s, _ = updateSettings(s, settingsActionFinishedMsg{err: assert.AnError})
	assert.Equal(t, settingsStateForm, s.state)
	assert.Error(t, s.err)
}

func TestSettings_ActionFinishedWithMessage(t *testing.T) {
	s := testSettings()
	s.state = settingsStateRunningAction

	s, _ = updateSettings(s, settingsActionFinishedMsg{message: "Success!"})
	assert.Equal(t, settingsStateActionComplete, s.state)
	assert.Equal(t, "Success!", s.actionSuccessMessage)
}

func TestSettings_ActionCompleteEnterNavigates(t *testing.T) {
	s := testSettings()
	s.state = settingsStateActionComplete

	_, cmd := updateSettings(s, keyPressMsg("enter"))
	require.NotNil(t, cmd)

	msg := cmd()
	_, ok := msg.(NavigateToDashboardMsg)
	assert.True(t, ok)
}

func TestSettings_ActionCompleteDoneClick(t *testing.T) {
	s := testSettings()
	s.state = settingsStateActionComplete

	_, cmd := updateSettings(s, MouseEvent{IsClick: true, Target: "done"})
	require.NotNil(t, cmd)

	msg := cmd()
	_, ok := msg.(NavigateToDashboardMsg)
	assert.True(t, ok)
}

func TestSettings_ErrorClearsOnKeypress(t *testing.T) {
	s := testSettings()
	s.state = settingsStateForm
	s.err = assert.AnError

	s, _ = updateSettings(s, keyPressMsg("a"))
	assert.Nil(t, s.err)
}

func TestSettings_ViewShowsError(t *testing.T) {
	s := testSettings()
	s.state = settingsStateForm
	s.err = assert.AnError
	s.width = 80
	s.height = 24

	view := s.View()
	assert.Contains(t, view, assert.AnError.Error())
}

func TestSettings_ViewShowsProgressWhileDeploying(t *testing.T) {
	s := testSettings()
	s.state = settingsStateDeploying
	s.width = 80
	s.height = 24

	// Should not panic
	view := s.View()
	assert.NotEmpty(t, view)
}

func TestSettings_ProgressMsgMovesBarWhileDeploying(t *testing.T) {
	s := testSettings()
	s, _ = updateSettings(s, tea.WindowSizeMsg{Width: 80, Height: 24})
	s.state = settingsStateDeploying

	s, cmd := updateSettings(s, progressUpdateMsg{progress: docker.DeployProgress{Stage: docker.DeployStageDownloading, Percentage: 42}})
	assert.Equal(t, 42, s.progress.percent)
	require.NotNil(t, cmd, "expected the wait command to be re-armed")

	s, _ = updateSettings(s, progressUpdateMsg{progress: docker.DeployProgress{Stage: docker.DeployStageStarting}})
	assert.Equal(t, -1, s.progress.percent)
}

func TestSettings_ProgressMsgIgnoredOnForm(t *testing.T) {
	s := testSettings()
	s, _ = updateSettings(s, tea.WindowSizeMsg{Width: 80, Height: 24})

	s, _ = updateSettings(s, progressUpdateMsg{progress: docker.DeployProgress{Stage: docker.DeployStageDownloading, Percentage: 42}})
	assert.Equal(t, -1, s.progress.percent)
}

func TestSettings_ActionReportsProgressThroughItsCallback(t *testing.T) {
	s := testSettings()
	s, _ = updateSettings(s, tea.WindowSizeMsg{Width: 80, Height: 24})

	action := func(progress docker.DeployProgressCallback) (string, error) {
		progress(docker.DeployProgress{Stage: docker.DeployStageDownloading, Percentage: 42})
		return "Update complete", nil
	}

	s, cmd := updateSettings(s, settingsRunActionMsg{action: action})
	require.Equal(t, settingsStateRunningAction, s.state)

	msgs := runBatch(t, cmd)
	assert.Contains(t, msgs, progressUpdateMsg{progress: docker.DeployProgress{Stage: docker.DeployStageDownloading, Percentage: 42}})
	assert.Contains(t, msgs, settingsActionFinishedMsg{message: "Update complete"})
}

// The apply path reports through the same plumbing; this pins the end of it, so
// a finished apply can never leave the wait command blocked on the channel.
func TestSettings_ApplyEndsTheProgressWait(t *testing.T) {
	s := testSettings()
	s, _ = updateSettings(s, tea.WindowSizeMsg{Width: 80, Height: 24})

	// An invalid Configuration fails inside ApplySettings before it needs
	// Docker, which keeps this a unit test.
	invalid := s.app.Settings
	invalid.Image = ""

	s, cmd := updateSettings(s, SettingsSectionSubmitMsg{Settings: invalid})
	require.Equal(t, settingsStateDeploying, s.state)

	msgs := runBatch(t, cmd)
	assert.Contains(t, msgs, settingsDeployFinishedMsg{err: docker.ErrImageRequired})
	assert.Contains(t, msgs, nil, "expected the wait command to end when the channel closed")
}

func TestSettings_IgnoresASecondRunWhileOneIsInFlight(t *testing.T) {
	s := testSettings()
	s, _ = updateSettings(s, tea.WindowSizeMsg{Width: 80, Height: 24})
	s.state = settingsStateRunningAction
	inFlight := newProgressStream()
	s.stream = inFlight

	s, cmd := updateSettings(s, settingsRunActionMsg{action: func(docker.DeployProgressCallback) (string, error) {
		return "", nil
	}})
	assert.Nil(t, cmd)
	assert.Same(t, inFlight, s.stream)
}

// A cancel landing mid-run used to discard this model, taking the drain loop
// and the handler for the run's result with it.
func TestSettings_CancelIsIgnoredWhileARunIsInFlight(t *testing.T) {
	for _, state := range []settingsState{settingsStateDeploying, settingsStateRunningAction} {
		s := testSettings()
		s.state = state

		s, cmd := updateSettings(s, SettingsSectionCancelMsg{})
		assert.Nil(t, cmd)
		assert.Equal(t, state, s.state)
	}
}

func TestSettings_CancelLeavesAnIdleForm(t *testing.T) {
	s := testSettings()

	_, cmd := updateSettings(s, SettingsSectionCancelMsg{})
	require.NotNil(t, cmd)
	assert.Equal(t, NavigateToDashboardMsg{AppName: s.app.Settings.Name}, cmd())
}

// Helpers

func testSettings() Settings {
	ns, _ := docker.NewNamespace("test")
	app := &docker.Application{
		Running: true,
		Settings: docker.ApplicationSettings{
			Name:  "test-app",
			Host:  "app.example.com",
			Image: "ghcr.io/basecamp/once-campfire:latest",
		},
	}

	return NewSettings(ns, app, SettingsSectionApplication)
}

func updateSettings(s Settings, msg tea.Msg) (Settings, tea.Cmd) {
	comp, cmd := s.Update(msg)
	return comp.(Settings), cmd
}

// runBatch collects the messages a batch produces. The commands run
// concurrently because the wait-for-progress command only returns once the
// task command has reported.
func runBatch(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	require.NotNil(t, cmd)

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok, "expected a tea.BatchMsg")

	produced := make(chan tea.Msg, len(batch))
	for _, c := range batch {
		go func() { produced <- c() }()
	}

	msgs := make([]tea.Msg, 0, len(batch))
	for range batch {
		select {
		case msg := <-produced:
			msgs = append(msgs, msg)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for batch commands")
		}
	}
	return msgs
}
