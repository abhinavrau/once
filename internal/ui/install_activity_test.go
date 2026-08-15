package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/basecamp/once/internal/catalog"
	"github.com/basecamp/once/internal/docker"
)

// Anchors the TUI install path to DefaultApplicationSettings. Fails when this
// path gains a non-zero default that isn't written there, keeping TUI installs
// and flagless CLI deploys (anchored by TestFlaglessDeploySettingsAreTheDefaults
// in internal/command) from drifting apart.
func TestInstallSettingsStartFromDefaults(t *testing.T) {
	s := installSettings(catalog.App{ImageRef: "image:latest"}, "books.localhost", docker.ExposureLocal)

	want := docker.DefaultApplicationSettings()
	want.Image = "image:latest"
	want.Host = "books.localhost"
	want.Exposure = docker.ExposureLocal
	assert.Equal(t, want, s)
}
