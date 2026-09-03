package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/catalog"
	"github.com/basecamp/once/internal/docker"
)

func TestApp_QuitExitsImmediatelyWhenScreenIsIdle(t *testing.T) {
	shutdownCalled := false
	app := &App{currentScreen: idleTestScreen{}, watchCancel: func() { shutdownCalled = true }}

	_, cmd := app.Update(keyPressMsg("ctrl+c"))
	require.NotNil(t, cmd)
	assert.Equal(t, tea.QuitMsg{}, cmd())
	assert.True(t, shutdownCalled)
}

func TestApp_QuitIsGuardedWhileScreenIsBusy(t *testing.T) {
	shutdownCalled := false
	app := &App{currentScreen: busyTestScreen{busy: true}, watchCancel: func() { shutdownCalled = true }}

	// The returned cmd schedules the notice's self-clearing tea.Tick; it must
	// not be invoked here since that would block for noticeDuration.
	_, cmd := app.Update(keyPressMsg("ctrl+c"))
	assert.NotNil(t, cmd)
	assert.False(t, shutdownCalled)
	assert.Equal(t, quitNoticeText, app.notice)
}

func TestApp_QuitExitsOnceTheBusyScreenGoesIdle(t *testing.T) {
	shutdownCalled := false
	app := &App{currentScreen: busyTestScreen{busy: false}, watchCancel: func() { shutdownCalled = true }}

	_, cmd := app.Update(keyPressMsg("ctrl+c"))
	require.NotNil(t, cmd)
	assert.Equal(t, tea.QuitMsg{}, cmd())
	assert.True(t, shutdownCalled)
}

// Guarding a running Settings action must not touch the run itself: the
// deploy goroutine keeps going, and its completion still lands normally once
// the guarded key press is swallowed.
func TestApp_QuitDuringSettingsActionDoesNotLoseCompletion(t *testing.T) {
	s := deployingSettings()
	require.True(t, s.Busy())
	app := &App{currentScreen: s, watchCancel: func() { t.Fatal("shutdown must not run while the deploy is in flight") }}

	_, cmd := app.Update(keyPressMsg("ctrl+c"))
	assert.NotNil(t, cmd)

	comp, navCmd := s.Update(settingsDeployFinishedMsg{})
	require.NotNil(t, navCmd)
	_, ok := navCmd().(NavigateToAppMsg)
	assert.True(t, ok)
	_, ok = comp.(Settings)
	assert.True(t, ok)
}

// QuitMsg is the second door to the same shutdown+quit action (dashboard's
// esc-bound quit, install's cliMode cancel): it must be guarded exactly like
// ctrl+c, or a busy screen reached through this door loses the protection.
func TestApp_QuitMsgIsGuardedWhileScreenIsBusy(t *testing.T) {
	app := &App{currentScreen: busyTestScreen{busy: true}, watchCancel: func() { t.Fatal("shutdown must not run while the screen is busy") }}

	_, cmd := app.Update(QuitMsg{})
	assert.NotNil(t, cmd)
	assert.Equal(t, quitNoticeText, app.notice)
}

// A guarded quit must not read as a hung TUI: swallowing the key silently
// gives no sign anything happened, so the guard also surfaces a visible
// notice explaining why quit did nothing.
func TestApp_QuitGuardShowsVisibleNoticeWhileScreenIsBusy(t *testing.T) {
	app := &App{
		currentScreen: busyTestScreen{busy: true},
		watchCancel:   func() { t.Fatal("shutdown must not run while the screen is busy") },
		lastSize:      tea.WindowSizeMsg{Width: 80, Height: 24},
	}

	_, cmd := app.Update(keyPressMsg("ctrl+c"))
	require.NotNil(t, cmd)
	assert.Contains(t, app.View().Content, "quit when done")
}

// The notice is brief: it clears itself once its scheduled tea.Tick fires,
// rather than staying on screen until the next user action.
func TestApp_QuitNoticeClearsAfterItExpires(t *testing.T) {
	app := &App{
		currentScreen: busyTestScreen{busy: true},
		watchCancel:   func() {},
		lastSize:      tea.WindowSizeMsg{Width: 80, Height: 24},
	}
	app.Update(keyPressMsg("ctrl+c"))
	require.Contains(t, app.View().Content, "quit when done")

	app.Update(noticeExpiredMsg{})
	assert.NotContains(t, app.View().Content, "quit when done")
}

// Install exposes the same quit path as Settings: both sit behind App's
// top-level ctrl+c handling, so a running install must be guarded exactly
// like a running settings action.
func TestApp_QuitDuringInstallActivityIsGuarded(t *testing.T) {
	m := newTestInstall()
	m, _ = updateInstall(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = updateInstall(m, InstallFormSubmitMsg{App: catalog.App{ImageRef: "ghcr.io/basecamp/once-campfire:latest"}, Hostname: "app.example.com", Exposure: docker.ExposurePublic})
	require.True(t, m.Busy())

	app := &App{currentScreen: m, watchCancel: func() { t.Fatal("shutdown must not run while the install is in flight") }}

	_, cmd := app.Update(keyPressMsg("ctrl+c"))
	assert.NotNil(t, cmd)
}

// An error is only ever on screen: the alt screen keeps no scrollback and
// mouse tracking takes selection away, so the copy key is the one way to get
// the message into an issue or a search box.
func TestApp_CopyKeyCopiesTheScreensError(t *testing.T) {
	app := &App{
		currentScreen: erroringTestScreen{message: "the daemon refused the connection"},
		lastSize:      tea.WindowSizeMsg{Width: 80, Height: 24},
	}

	_, cmd := app.Update(keyPressMsg("ctrl+y"))
	require.NotNil(t, cmd)
	assert.Contains(t, app.View().Content, "Copied")
}

// With nothing to copy the key stays the screen's own, rather than being
// swallowed on every frame that has no error.
func TestApp_CopyKeyReachesTheScreenWithoutAnError(t *testing.T) {
	screen := erroringTestScreen{}
	app := &App{currentScreen: screen, lastSize: tea.WindowSizeMsg{Width: 80, Height: 24}}

	_, cmd := app.Update(keyPressMsg("ctrl+y"))
	require.NotNil(t, cmd)
	assert.Equal(t, "ctrl+y", cmd().(string))
	assert.Empty(t, app.notice)
}

// Helpers

type idleTestScreen struct{}

func (idleTestScreen) Init() tea.Cmd                       { return nil }
func (idleTestScreen) Update(tea.Msg) (Component, tea.Cmd) { return idleTestScreen{}, nil }
func (idleTestScreen) View() string                        { return "" }

// erroringTestScreen reports the key it was handed, so a key App did not
// claim can be told from one it swallowed.
type erroringTestScreen struct{ message string }

func (s erroringTestScreen) Init() tea.Cmd { return nil }
func (s erroringTestScreen) Update(msg tea.Msg) (Component, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		return s, func() tea.Msg { return key.String() }
	}
	return s, nil
}
func (s erroringTestScreen) View() string          { return "" }
func (s erroringTestScreen) CopyableError() string { return s.message }

type busyTestScreen struct{ busy bool }

func (s busyTestScreen) Init() tea.Cmd                       { return nil }
func (s busyTestScreen) Update(tea.Msg) (Component, tea.Cmd) { return s, nil }
func (s busyTestScreen) View() string                        { return "" }
func (s busyTestScreen) Busy() bool                          { return s.busy }
