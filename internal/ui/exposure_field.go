package ui

import (
	"github.com/basecamp/once/internal/docker"
)

// exposureChoices is the single source for the radio's option order and labels;
// a field's selected index is an index into this slice.
var exposureChoices = []struct {
	exposure docker.Exposure
	label    string
}{
	{docker.ExposureLocal, "Local"},
	{docker.ExposureTailnet, "Tailnet"},
	{docker.ExposurePublic, "Public"},
}

func NewExposureRadioField() *RadioField {
	labels := make([]string, len(exposureChoices))
	for i, choice := range exposureChoices {
		labels[i] = choice.label
	}
	return NewRadioField(labels...)
}

// Helpers

func exposureAt(selected int) docker.Exposure {
	return exposureChoices[selected].exposure
}

func exposureIndex(exposure docker.Exposure) int {
	for i, choice := range exposureChoices {
		if choice.exposure == exposure {
			return i
		}
	}
	return 0
}

// exposureHostSuggestion derives the default host for an exposure from the app
// name, matching the CLI's empty --host behavior. Public has no default: the
// user must name a real FQDN.
func exposureHostSuggestion(exposure docker.Exposure, appName string) string {
	host, err := docker.ResolveHost(exposure, "", appName)
	if err != nil {
		return ""
	}
	return host
}
