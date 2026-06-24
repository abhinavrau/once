package ui

import (
	"strings"
	"testing"

	"github.com/basecamp/once/internal/docker"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettingsMenu_TitleCentered(t *testing.T) {
	app := &docker.Application{}
	m := NewSettingsMenu(app)
	view := ansi.Strip(m.View())
	lines := strings.Split(view, "\n")

	titleOffset := -1
	menuOffset := -1
	for _, line := range lines {
		if i := strings.Index(line, "Settings"); i >= 0 {
			titleOffset = i
		}
		if i := strings.Index(line, "Application"); i >= 0 {
			menuOffset = i
		}
	}

	require.GreaterOrEqual(t, titleOffset, 0)
	require.GreaterOrEqual(t, menuOffset, 0)
	assert.Greater(t, titleOffset, menuOffset)
}

func TestSettingsMenu_HasTailscaleFunnel(t *testing.T) {
	m := NewSettingsMenu(&docker.Application{})
	assert.Contains(t, ansi.Strip(m.View()), "Tailscale Funnel")
}
