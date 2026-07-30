package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
)

const (
	progressFilled = '⣿'
	progressTrack  = '⣀'
)

// A completed pull used to flip the bar back to the indeterminate animation,
// because only a percent below 100 drew the determinate bar.
func TestProgress_FullPercentRendersAFullBar(t *testing.T) {
	assert.Equal(t, strings.Repeat(string(progressFilled), 10), progressBar(t, 100))
}

func TestProgress_ZeroPercentRendersAnEmptyBar(t *testing.T) {
	assert.Equal(t, strings.Repeat(string(progressTrack), 10), progressBar(t, 0))
}

func TestProgress_PartialPercentFillsFromTheLeft(t *testing.T) {
	bar := progressBar(t, 50)
	assert.Equal(t, strings.Repeat(string(progressFilled), 5)+strings.Repeat(string(progressTrack), 5), bar)
}

func TestProgress_NegativePercentDrawsTheAnimatedPattern(t *testing.T) {
	pattern := strings.Repeat("⠿", 10)
	p := NewProgress(10, Colors.Primary).SetPercent(-1)
	p.pattern = []rune(pattern)

	assert.Equal(t, pattern, ansi.Strip(p.View()))
}

// Helpers

// progressBar strips the styling so the assertions read against the glyphs.
func progressBar(t *testing.T, percent int) string {
	t.Helper()
	return ansi.Strip(NewProgress(10, Colors.Primary).SetPercent(percent).View())
}
