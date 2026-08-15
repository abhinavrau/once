package ui

import (
	"os"

	"github.com/charmbracelet/colorprofile"
)

// colorProfile is the profile the running bubbletea program renders with.
// Colours are downsampled at the writer, not at Render, so this - and not the
// style - is what decides whether a forced colour survives to the screen.
//
// It defaults to TrueColor so tests and callers that never start a program see
// undegraded output; the entry points that do start one record what they
// detected.
var colorProfile = colorprofile.TrueColor

func ColorProfile() colorprofile.Profile { return colorProfile }

func SetColorProfile(profile colorprofile.Profile) { colorProfile = profile }

// DetectColorProfile resolves the profile a program on this terminal will
// render with: the OSC probe is the authority when it succeeded, and the
// environment is the fallback, which is the same order Run applies when it
// decides whether to force truecolor on the program.
func DetectColorProfile(detected DetectedColors) colorprofile.Profile {
	if detected.SupportsTrueColor() {
		return colorprofile.TrueColor
	}
	return colorprofile.Detect(os.Stdout, os.Environ())
}

// Helpers

func lookupEnv(name string) string { return os.Getenv(name) }
