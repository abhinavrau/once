package docker

import (
	"context"
	"fmt"
	"regexp"
	"slices"
)

// dnsLabelPattern is an RFC 1123 DNS label: what a service name must be to be
// reachable as <name>.<tailnet-suffix> once advertised as a Tailscale Service.
var dnsLabelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// ValidateTailscaleServiceName rejects a name that cannot be a Magic DNS
// label, since Docktail would advertise it as <name>.<suffix> and the entry
// could never resolve.
func ValidateTailscaleServiceName(name string) error {
	if len(name) > 63 || !dnsLabelPattern.MatchString(name) {
		return fmt.Errorf("service name %q is not a valid DNS label: use 1-63 lowercase letters, digits, or hyphens (not at the start or end)", name)
	}
	return nil
}

// ShadowWarnings reports, for each service name, whether an existing tailnet
// machine's Magic DNS entry shadows it: machine names win over Services at
// the same FQDN, so the app URL would silently resolve to the machine.
// Best effort by design - this guards a warning, so an unreadable daemon
// (or Tailscale not enabled) yields no warnings rather than failing the
// enable or deploy that asked.
func (t *Tailscale) ShadowWarnings(ctx context.Context, names []string) []string {
	_, daemon, err := t.session(ctx)
	if err != nil {
		return nil
	}
	return shadowWarnings(ctx, daemon, names)
}

// Helpers

func shadowWarnings(ctx context.Context, daemon TailscaleDaemon, names []string) []string {
	machines, err := daemon.MachineHostnames(ctx)
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
