package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"

	"github.com/basecamp/once/internal/docker"
	"github.com/basecamp/once/internal/metrics"
)

func TestDashboardPanelViewRunningApp(t *testing.T) {
	panel := testPanel(true)
	view := panel.View(false, false, true, 120, DashboardScales{})
	stripped := ansi.Strip(view)

	assert.Contains(t, stripped, "app.example.com")
	assert.Contains(t, stripped, "running")
}

func TestDashboardPanelViewStoppedApp(t *testing.T) {
	panel := testPanel(false)
	view := panel.View(false, false, true, 120, DashboardScales{})
	stripped := ansi.Strip(view)

	assert.Contains(t, stripped, "app.example.com")
	assert.Contains(t, stripped, "stopped")
}

func TestDashboardPanelHeightRunning(t *testing.T) {
	panel := testPanel(true)

	fullHeight := panel.Height(true)
	compactHeight := panel.Height(false)

	assert.Equal(t, PanelHeight+2, fullHeight)
	assert.Equal(t, StoppedPanelHeight+2, compactHeight)
}

func TestDashboardPanelHeightStopped(t *testing.T) {
	panel := testPanel(false)

	// Stopped always uses compact height regardless of showDetails
	assert.Equal(t, StoppedPanelHeight+2, panel.Height(true))
	assert.Equal(t, StoppedPanelHeight+2, panel.Height(false))
}

func TestDashboardPanelSelectedHasIndicator(t *testing.T) {
	panel := testPanel(true)
	view := panel.View(true, false, true, 80, DashboardScales{})

	// Selected panels have the indicator character
	assert.Contains(t, view, "▐")
	assert.Contains(t, view, "▗")
	assert.Contains(t, view, "▝")
}

func TestDashboardPanelNotSelectedNoIndicator(t *testing.T) {
	panel := testPanel(true)
	view := panel.View(false, false, true, 80, DashboardScales{})

	assert.NotContains(t, view, "▐")
	assert.NotContains(t, view, "▗")
	assert.NotContains(t, view, "▝")
}

func TestDashboardPanelNarrowWidthHidesCards(t *testing.T) {
	panel := testPanel(true)
	// Width of 30 is too narrow for cards (need minCardWidth*4+3 = 35 inner)
	view := panel.View(false, false, true, 30, DashboardScales{})

	// Should not contain card borders
	assert.NotContains(t, view, "╭─")
}

func TestDashboardPanelWideWidthShowsCards(t *testing.T) {
	panel := testPanel(true)
	view := panel.View(false, false, true, 120, DashboardScales{})

	// Cards should have borders
	assert.Contains(t, view, "╭─")
	assert.Contains(t, view, "╰")
}

func TestDashboardPanelTogglingShowsStatus(t *testing.T) {
	panel := testPanel(true)
	view := panel.View(false, true, true, 120, DashboardScales{})
	stripped := ansi.Strip(view)

	assert.Contains(t, stripped, "stopping...")
}

func TestDashboardPanelViewHasThreeLines(t *testing.T) {
	panel := testPanel(false)
	view := panel.View(false, false, false, 80, DashboardScales{})
	lines := strings.Split(view, "\n")

	// top transition + body lines + bottom transition
	assert.GreaterOrEqual(t, len(lines), 3)
}

func TestRenderStateInfo(t *testing.T) {
	running := &docker.Application{Running: true}
	stopped := &docker.Application{Running: false}

	assert.Contains(t, ansi.Strip(renderStateInfo(running, false)), "running")
	assert.Contains(t, ansi.Strip(renderStateInfo(stopped, false)), "stopped")
	assert.Contains(t, ansi.Strip(renderStateInfo(running, true)), "stopping...")
	assert.Contains(t, ansi.Strip(renderStateInfo(stopped, true)), "starting...")
}

func TestRenderBar(t *testing.T) {
	bar := renderBar(50, 0, 100, Colors.Success, 10)
	assert.NotEmpty(t, bar)
	// Bar contains braille characters
	assert.Contains(t, bar, "⢾")
	assert.Contains(t, bar, "⡷")
}

func TestRenderBarZeroWidth(t *testing.T) {
	assert.Empty(t, renderBar(50, 0, 100, Colors.Success, 0))
}

func TestDashboardPanelShowsTailnetRowsWhenEnabled(t *testing.T) {
	panel := testPanel(true)
	panel.tailscaleEnabled = true
	panel.tailnetURL = "https://books.tailnet.ts.net"

	view := ansi.Strip(panel.View(false, false, true, 120, DashboardScales{}))

	assert.Contains(t, view, "Public/Local URL")
	assert.Contains(t, view, "Tailnet URL")
	assert.Contains(t, view, "https://books.tailnet.ts.net")
	assert.Contains(t, view, "Funnel Status")
	assert.Contains(t, view, "Inactive")
}

func TestDashboardPanelOmitsTailnetRowsWhenDisabled(t *testing.T) {
	panel := testPanel(true) // tailscaleEnabled defaults false
	view := ansi.Strip(panel.View(false, false, true, 120, DashboardScales{}))

	assert.NotContains(t, view, "Tailnet URL")
}

func TestDashboardPanelOmitsTailnetRowsWhenExcluded(t *testing.T) {
	panel := testPanel(true)
	panel.tailscaleEnabled = true
	panel.app.Settings.TailscaleExcluded = true

	view := ansi.Strip(panel.View(false, false, true, 120, DashboardScales{}))
	assert.NotContains(t, view, "Tailnet URL")
}

func TestDashboardPanelHeightGrowsWithTailnetRows(t *testing.T) {
	panel := testPanel(true)
	panel.tailscaleEnabled = true

	assert.Equal(t, PanelHeight+tailnetRowCount+2, panel.Height(true))
	// Collapsed details never show tailnet rows.
	assert.Equal(t, StoppedPanelHeight+2, panel.Height(false))
}

func TestDashboardPanelShowsActiveFunnel(t *testing.T) {
	panel := testPanel(true)
	panel.tailscaleEnabled = true
	expiresAt := time.Now().Add(20 * time.Minute)
	panel.app.Settings.FunnelExpiresAt = &expiresAt

	view := ansi.Strip(panel.View(false, false, true, 120, DashboardScales{}))
	assert.Contains(t, view, "Active (Expires in")
}

// Helpers

func testPanel(running bool) DashboardPanel {
	app := &docker.Application{
		Running: running,
		Settings: docker.ApplicationSettings{
			Name:  "test-app",
			Host:  "app.example.com",
			Image: "ghcr.io/basecamp/test-app:latest",
		},
	}
	scraper := metrics.NewMetricsScraper(metrics.ScraperSettings{})
	dockerScraper := &docker.Scraper{}

	return NewDashboardPanel(app, scraper, dockerScraper, nil)
}
