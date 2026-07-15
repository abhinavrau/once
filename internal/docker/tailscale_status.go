package docker

import (
	"context"
	"fmt"
	"slices"
)

// ServiceStatus is one row of `once tailscale status`: a service's tailnet
// URL and its advertised/approved state rendered for display.
type ServiceStatus struct {
	Name  string
	URL   string
	State string
}

// ServiceStatuses reports each named service's current state: advertised
// according to the local daemon's serve status, and approved according to the
// Tailscale cloud API. On the headscale control seam only advertisement is
// checked - the cloud endpoints are Tailscale SaaS only.
func (t *Tailscale) ServiceStatuses(ctx context.Context, names []string) ([]ServiceStatus, error) {
	settings, err := t.LoadSettings(ctx)
	if err != nil {
		return nil, err
	}
	return serviceStatuses(ctx, t.daemonFor(settings.Mode), tailscaleAPIBaseURL, settings, names)
}

// Helpers

func serviceStatuses(ctx context.Context, daemon TailscaleDaemon, baseURL string, settings TailscaleSettings, names []string) ([]ServiceStatus, error) {
	serve, err := daemon.ServeStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading serve status from the local tailscaled: %w", err)
	}

	var token string
	if !controlSeamActive() {
		if token, err = tailscaleOAuthToken(ctx, baseURL, settings.ClientID, settings.ClientSecret); err != nil {
			return nil, err
		}
	}

	statuses := make([]ServiceStatus, 0, len(names))
	for _, name := range names {
		advertised := slices.Contains(serve.Services, name)
		approval := serviceUnknown
		if token != "" && advertised {
			if approval, err = serviceApprovalState(ctx, baseURL, token, name); err != nil {
				return nil, err
			}
		}
		statuses = append(statuses, ServiceStatus{
			Name:  name,
			URL:   settings.TailnetURL(name),
			State: renderServiceState(advertised, approval, token != "", settings.Tag),
		})
	}
	return statuses, nil
}

// renderServiceState turns the two verification sources into one display
// state. Advertisement is the local daemon's fact, approval the cloud's: an
// unadvertised service is unreachable no matter what the tailnet thinks, and
// a pending one needs a human (or an autoApprovers rule), so each gets its
// own actionable message.
func renderServiceState(advertised bool, approval approvalState, cloudChecked bool, tag string) string {
	switch {
	case !advertised:
		return "not advertised (check the once-docktail container logs)"
	case !cloudChecked:
		return "advertised"
	case approval == serviceApproved:
		return "approved"
	case approval == servicePending:
		return fmt.Sprintf("pending approval (approve it in the Tailscale admin console, or add an autoApprovers.services rule for %s)", tag)
	default:
		return "advertised, but not yet in the tailnet (check the once-docktail container logs)"
	}
}
