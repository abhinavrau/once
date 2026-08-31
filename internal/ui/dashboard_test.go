package ui

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
	"github.com/basecamp/once/internal/logging"
	"github.com/basecamp/once/internal/metrics"
	"github.com/basecamp/once/internal/system"
)

func TestFormatDuration(t *testing.T) {
	t.Run("seconds", func(t *testing.T) {
		assert.Equal(t, "0s", formatDuration(0))
		assert.Equal(t, "1s", formatDuration(1*time.Second))
		assert.Equal(t, "45s", formatDuration(45*time.Second))
		assert.Equal(t, "59s", formatDuration(59*time.Second))
	})

	t.Run("minutes", func(t *testing.T) {
		assert.Equal(t, "1m", formatDuration(1*time.Minute))
		assert.Equal(t, "30m", formatDuration(30*time.Minute))
		assert.Equal(t, "59m", formatDuration(59*time.Minute))
		assert.Equal(t, "1m", formatDuration(1*time.Minute+30*time.Second))
	})

	t.Run("hours", func(t *testing.T) {
		assert.Equal(t, "1h", formatDuration(1*time.Hour))
		assert.Equal(t, "2h", formatDuration(2*time.Hour))
		assert.Equal(t, "3h 45m", formatDuration(3*time.Hour+45*time.Minute))
		assert.Equal(t, "23h 59m", formatDuration(23*time.Hour+59*time.Minute))
	})

	t.Run("days", func(t *testing.T) {
		assert.Equal(t, "1d", formatDuration(24*time.Hour))
		assert.Equal(t, "2d", formatDuration(48*time.Hour))
		assert.Equal(t, "1d 1h", formatDuration(25*time.Hour))
		assert.Equal(t, "2d 2h", formatDuration(50*time.Hour))
		assert.Equal(t, "7d 12h", formatDuration(7*24*time.Hour+12*time.Hour))
	})
}

func TestDashboardKeyboardSelectsPanel(t *testing.T) {
	d := testDashboard(3)
	d.width = 80
	d.height = 40
	d.updateViewportSize()
	d.rebuildViewportContent()

	assert.Equal(t, 0, d.selectedIndex)

	d, _ = updateDashboard(d, keyPressMsg("down"))
	assert.Equal(t, 1, d.selectedIndex)

	d, _ = updateDashboard(d, keyPressMsg("down"))
	assert.Equal(t, 2, d.selectedIndex)

	// Can't go past last
	d, _ = updateDashboard(d, keyPressMsg("down"))
	assert.Equal(t, 2, d.selectedIndex)

	d, _ = updateDashboard(d, keyPressMsg("up"))
	assert.Equal(t, 1, d.selectedIndex)

	d, _ = updateDashboard(d, keyPressMsg("up"))
	assert.Equal(t, 0, d.selectedIndex)

	// Can't go before first
	d, _ = updateDashboard(d, keyPressMsg("up"))
	assert.Equal(t, 0, d.selectedIndex)
}

func TestDashboardKeyboardJK(t *testing.T) {
	d := testDashboard(3)
	d.width = 80
	d.height = 40
	d.updateViewportSize()
	d.rebuildViewportContent()

	d, _ = updateDashboard(d, keyPressMsg("j"))
	assert.Equal(t, 1, d.selectedIndex)

	d, _ = updateDashboard(d, keyPressMsg("k"))
	assert.Equal(t, 0, d.selectedIndex)
}

func TestDashboard_ActionsMenuOpensOnA(t *testing.T) {
	d := testDashboard(1)
	d.width = 80
	d.height = 40
	d.updateViewportSize()
	d.rebuildViewportContent()

	d, _ = updateDashboard(d, keyPressMsg("a"))
	assert.NotNil(t, d.overlay)
}

func TestDashboard_ActionsMenuCloses(t *testing.T) {
	d := testDashboard(1)
	d.width = 80
	d.height = 40
	d.updateViewportSize()
	d.rebuildViewportContent()

	d, _ = updateDashboard(d, keyPressMsg("a"))
	assert.NotNil(t, d.overlay)

	d, _ = updateDashboard(d, ActionsMenuCloseMsg{})
	assert.Nil(t, d.overlay)
}

func TestDashboard_ActionsMenuStartStop(t *testing.T) {
	d := testDashboard(1)
	d.width = 80
	d.height = 40
	d.updateViewportSize()
	d.rebuildViewportContent()

	d, _ = updateDashboard(d, ActionsMenuSelectMsg{app: d.apps[0], action: ActionsMenuStartStop})
	assert.True(t, d.toggling)
}

func TestDashboard_ActionsMenuRemove(t *testing.T) {
	d := testDashboard(1)
	d.width = 80
	d.height = 40
	d.updateViewportSize()
	d.rebuildViewportContent()

	_, cmd := updateDashboard(d, ActionsMenuSelectMsg{app: d.apps[0], action: ActionsMenuRemove})
	require.NotNil(t, cmd)

	msg := cmd()
	navMsg, ok := msg.(NavigateToRemoveMsg)
	require.True(t, ok, "expected NavigateToRemoveMsg, got %T", msg)
	assert.Equal(t, d.apps[0], navMsg.App)
}

func TestDashboard_OldStartStopKeyRemoved(t *testing.T) {
	d := testDashboard(1)
	d.width = 80
	d.height = 40
	d.updateViewportSize()
	d.rebuildViewportContent()

	d, _ = updateDashboard(d, keyPressMsg("o"))
	assert.False(t, d.toggling)
}

func TestDashboard_SettingsMenuStillWorks(t *testing.T) {
	d := testDashboard(1)
	d.width = 80
	d.height = 40
	d.updateViewportSize()
	d.rebuildViewportContent()

	d, _ = updateDashboard(d, keyPressMsg("s"))
	assert.NotNil(t, d.overlay)
}

func TestDashboard_ToggleDetails(t *testing.T) {
	d := testDashboard(2)
	d.width = 120
	d.height = 40
	d.updateViewportSize()
	d.rebuildViewportContent()

	assert.True(t, dashboardShowDetails)
	fullHeight := d.panels[0].Height(dashboardShowDetails)

	d, _ = updateDashboard(d, keyPressMsg("d"))
	assert.False(t, dashboardShowDetails)
	compactHeight := d.panels[0].Height(dashboardShowDetails)
	assert.Less(t, compactHeight, fullHeight)

	// Toggle back
	d, _ = updateDashboard(d, keyPressMsg("d"))
	assert.True(t, dashboardShowDetails)
}

func TestDashboard_ToggleDetailsEmptyState(t *testing.T) {
	d := testDashboard(0)
	d, _ = updateDashboard(d, tea.WindowSizeMsg{Width: 80, Height: 24})

	// Should not panic
	_, _ = updateDashboard(d, keyPressMsg("d"))
	assert.True(t, dashboardShowDetails) // unchanged — guarded by len(m.apps) > 0
}

func TestDashboard_SelectPanelAfterAppsRemoved(t *testing.T) {
	d := testDashboard(3)
	d.width = 80
	d.height = 40
	d.updateViewportSize()
	d.rebuildViewportContent()

	// Simulate all apps disappearing (e.g. `once teardown` in another shell).
	d.apps = nil
	d.buildPanels()

	// Regression: #49 — scrollToSelection used to index into an empty m.panels.
	assert.NotPanics(t, func() {
		d.selectPanel(0)
	})
}

func TestDashboard_EmptyStateShowsMessage(t *testing.T) {
	d := testDashboard(0)
	d, _ = updateDashboard(d, tea.WindowSizeMsg{Width: 80, Height: 24})

	view := d.View()
	assert.Contains(t, view, "There are no applications installed")
	assert.Contains(t, view, "new app")
}

func TestDashboard_VerboseHintInFooter(t *testing.T) {
	d := testDashboard(0)
	d, _ = updateDashboard(d, tea.WindowSizeMsg{Width: 120, Height: 24})

	assert.NotContains(t, d.View(), "verbose log:")

	t.Cleanup(func() { logging.SetVerbose(false) })
	logging.SetVerbose(true)
	view := d.View()
	assert.Contains(t, view, "verbose log:")
	assert.Contains(t, view, "once.log")
}

func TestDashboard_UnresolvableTailnetURLIsMarkedAndExplained(t *testing.T) {
	stubLookupHost(t, nil, errors.New("server misbehaving"))
	stubHostTailnetSuffix(t, "example.com.tailnet.ts.net", true)

	d := tailnetDashboard(t)
	view := ansi.Strip(d.View())

	assert.Contains(t, view, "https://app-0.example.com.tailnet.ts.net ⚠")
	assert.Contains(t, view, "app-0.example.com.tailnet.ts.net does not resolve from this machine")
	assert.Contains(t, view, "MagicDNS may be broken")
}

func TestDashboard_ResolvableTailnetURLIsNotMarked(t *testing.T) {
	stubLookupHost(t, []string{"100.64.0.1"}, nil)

	d := tailnetDashboard(t)
	view := ansi.Strip(d.View())

	assert.Contains(t, view, "https://app-0.example.com.tailnet.ts.net")
	assert.NotContains(t, view, "⚠")
	assert.NotContains(t, view, "does not resolve from this machine")
}

func TestDashboard_UncheckedTailnetURLIsNotMarked(t *testing.T) {
	failOnLookup(t)

	// Tailscale info that arrives before any check has completed carries no
	// verdicts; an unknown one must not read as a warning.
	d := testDashboard(1)
	d.apps[0].Settings.Exposure = docker.ExposureTailnet
	d.buildPanels()
	d, _ = updateDashboard(d, tea.WindowSizeMsg{Width: 120, Height: 40})
	d, _ = updateDashboard(d, tailscaleInfoMsg{enabled: true, settings: docker.TailscaleSettings{DomainSuffix: "tailnet.ts.net"}})

	view := ansi.Strip(d.View())
	assert.Contains(t, view, "https://app-0.example.com.tailnet.ts.net")
	assert.NotContains(t, view, "⚠")
}

func TestDashboard_TailnetWarningNoteShrinksTheViewport(t *testing.T) {
	stubLookupHost(t, nil, errors.New("server misbehaving"))
	stubHostTailnetSuffix(t, "example.com.tailnet.ts.net", true)

	plain := testDashboard(1)
	plain, _ = updateDashboard(plain, tea.WindowSizeMsg{Width: 120, Height: 40})

	warned := tailnetDashboard(t)
	noteHeight := lipgloss.Height(warned.tailnetWarningNote())

	assert.Positive(t, noteHeight)
	assert.Equal(t, plain.viewport.Height()-noteHeight, warned.viewport.Height())
	assert.Equal(t, lipgloss.Height(plain.View()), lipgloss.Height(warned.View()))
}

func TestDashboardTailnetDNSWarningsSkipsHostsThatResolve(t *testing.T) {
	stubLookupHost(t, nil, errors.New("server misbehaving"))
	stubHostTailnetSuffix(t, "tailnet.ts.net", true)

	settings := docker.TailscaleSettings{DomainSuffix: "tailnet.ts.net"}
	warnings := tailnetDNSWarnings(context.Background(), []string{"books", "chat"}, settings, nil)

	assert.Len(t, warnings, 2)
	assert.Contains(t, warnings["https://books.tailnet.ts.net"], "books.tailnet.ts.net does not resolve")
	assert.Contains(t, warnings["https://chat.tailnet.ts.net"], "chat.tailnet.ts.net does not resolve")

	stubLookupHost(t, []string{"100.64.0.1"}, nil)
	assert.Empty(t, tailnetDNSWarnings(context.Background(), []string{"books"}, settings, nil))
}

// TestDashboardTailnetDNSWarningsChecksTheDedupedName pins that the card
// resolves the name the node actually answers on. Composing books.<suffix>
// when Tailscale assigned books-1 warns about the wrong host - and the card's
// link points at the same wrong host.
func TestDashboardTailnetDNSWarningsChecksTheDedupedName(t *testing.T) {
	stubLookupHost(t, nil, errors.New("server misbehaving"))
	stubHostTailnetSuffix(t, "tailnet.ts.net", true)

	settings := docker.TailscaleSettings{DomainSuffix: "tailnet.ts.net"}
	fqdns := map[string]string{"books": "books-1.tailnet.ts.net"}

	warnings := tailnetDNSWarnings(context.Background(), []string{"books"}, settings, fqdns)

	assert.Contains(t, warnings["https://books-1.tailnet.ts.net"], "books-1.tailnet.ts.net does not resolve")
	assert.NotContains(t, warnings, "https://books.tailnet.ts.net")
}

func TestDashboard_OlderTailscaleSetupShowsTheFacadeMessageVerbatim(t *testing.T) {
	d := testDashboard(1)
	d, _ = updateDashboard(d, tea.WindowSizeMsg{Width: 120, Height: 40})

	d, _ = updateDashboard(d, tailscaleInfoMsg{enabled: true, setupWarning: docker.ErrOlderOnceSetup.Error()})

	// The note word-wraps at the dashboard's width, so compare with
	// whitespace normalized rather than asserting a literal substring.
	view := normalizeWhitespace(ansi.Strip(d.View()))
	assert.Contains(t, view, normalizeWhitespace(docker.ErrOlderOnceSetup.Error()))
}

func TestDashboardResolvableTailnetHostsSkipsStoppedAndNonTailnetApps(t *testing.T) {
	d := testDashboard(3)
	d.apps[0].Settings.Exposure = docker.ExposureTailnet
	d.apps[1].Settings.Exposure = docker.ExposureTailnet
	d.apps[1].Running = false
	d.apps[2].Settings.Exposure = docker.ExposureLocal

	assert.Equal(t, []string{"app-0.example.com"}, d.resolvableTailnetHosts())
}

// Helpers

// tailnetDashboard is a sized dashboard with one running tailnet-exposed app
// whose service URL has been checked against the stubbed resolver.
func tailnetDashboard(t *testing.T) Dashboard {
	t.Helper()

	d := testDashboard(1)
	d.apps[0].Settings.Exposure = docker.ExposureTailnet
	d.buildPanels()
	d, _ = updateDashboard(d, tea.WindowSizeMsg{Width: 120, Height: 40})

	settings := docker.TailscaleSettings{DomainSuffix: "tailnet.ts.net"}
	d, _ = updateDashboard(d, tailscaleInfoMsg{
		enabled:  true,
		settings: settings,
		warnings: tailnetDNSWarnings(context.Background(), d.resolvableTailnetHosts(), settings, nil),
	})
	return d
}

func testDashboard(numApps int) Dashboard {
	apps := make([]*docker.Application, numApps)
	for i := range numApps {
		apps[i] = &docker.Application{
			Running: true,
			Settings: docker.ApplicationSettings{
				Name: fmt.Sprintf("app-%d", i),
				Host: fmt.Sprintf("app-%d.example.com", i),
			},
		}
	}

	scraper := metrics.NewMetricsScraper(metrics.ScraperSettings{})
	dockerScraper := &docker.Scraper{}
	systemScraper := system.NewScraper(system.ScraperSettings{BufferSize: 10})

	dashboardShowDetails = true
	return NewDashboard(nil, apps, 0, scraper, dockerScraper, systemScraper, nil)
}

func updateDashboard(d Dashboard, msg tea.Msg) (Dashboard, tea.Cmd) {
	comp, cmd := d.Update(msg)
	return comp.(Dashboard), cmd
}
