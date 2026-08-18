package docker

import (
	"context"
	"fmt"
	"slices"
)

// Helpers

// shadowWarnings reports, for each service name, whether an existing tailnet
// machine's Magic DNS entry shadows it: machine names win over Services at
// the same FQDN, so the app URL would silently resolve to the machine.
// Best effort by design - this guards a warning, so an unreadable daemon
// (or Tailscale not enabled) yields no warnings rather than failing the
// enable or deploy that asked. Topology-agnostic: any TailnetReader's
// MachineHostnames works the same way, so this stays a facade concern
// (Tailscale.ShadowWarnings, tailscale.go) rather than a seam method.
func shadowWarnings(ctx context.Context, reader TailnetReader, names []string) []string {
	machines, err := reader.MachineHostnames(ctx)
	if err != nil {
		return nil
	}

	var warnings []string
	for _, name := range names {
		if slices.Contains(machines, name) {
			warnings = append(warnings, fmt.Sprintf("the tailnet machine %q shadows the service %q: Magic DNS resolves the app's URL to that machine, not the app; rename the machine to reach the app", name, name))
		}
	}
	return warnings
}
