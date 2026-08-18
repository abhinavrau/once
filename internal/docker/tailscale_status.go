package docker

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

// ServiceStatus is one row of `once tailscale status`: a service's tailnet
// URL and its advertised/approved state rendered for display. FunnelURL and
// FunnelExpiresAt are set only while the service has an active Funnel, giving
// its public address and when that access closes.
type ServiceStatus struct {
	Name            string     `json:"name"`
	URL             string     `json:"url"`
	State           string     `json:"state"`
	FunnelURL       string     `json:"funnelURL,omitempty"`
	FunnelExpiresAt *time.Time `json:"funnelExpiresAt,omitempty"`
}

// ServiceStatuses reports each named service's current state: advertised
// according to the local daemon's serve status, and approved according to the
// Tailscale cloud API. On the headscale control seam only advertisement is
// checked - the cloud endpoints are Tailscale SaaS only. On the SaaS path the
// credentials are proven once before any service is reported, so a revoked
// credential fails the command instead of being mistaken for unadvertised
// services.
func (t *Tailscale) ServiceStatuses(ctx context.Context, names []string) ([]ServiceStatus, error) {
	settings, daemon, err := t.session(ctx)
	if err != nil {
		return nil, err
	}
	return serviceStatuses(ctx, daemon, newTailscaleAPI(settings), settings, names, t.logsHint())
}

// Helpers

func serviceStatuses(ctx context.Context, daemon TailnetReader, api tailscaleAPI, settings TailscaleSettings, names []string, hint string) ([]ServiceStatus, error) {
	serve, err := daemon.ServeStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading serve status from the local tailscaled: %w", err)
	}

	cloudChecked := !controlSeamActive()
	if cloudChecked {
		if err := checkOAuthCredentials(ctx, api); err != nil {
			return nil, err
		}
	}

	statuses := make([]ServiceStatus, 0, len(names))
	for _, name := range names {
		advertised := slices.Contains(serve.Services, name)
		approval := serviceUnknown
		if cloudChecked && advertised {
			if approval, err = serviceApprovalState(ctx, api, name); err != nil {
				return nil, err
			}
		}
		statuses = append(statuses, ServiceStatus{
			Name:  name,
			URL:   settings.TailnetURL(name),
			State: renderServiceState(advertised, approval, cloudChecked, settings.Tag, hint),
		})
	}
	return statuses, nil
}

// checkOAuthCredentials proves the OAuth credentials before any service state
// is reported, via the seam's token-only Authenticate (ADR-0011) - so the
// check exchanges the token alone and never touches tailnet data it does not
// need, and cannot fail for tailnet-shaped reasons. Without a call of its own,
// a revoked credential would only be noticed wherever the per-service cloud
// check happens to run - never, when nothing is advertised, and mid-listing
// when only some services are. Everything but a token endpoint that is merely
// rate-limited or broken is fatal here, forwarded unchanged: that one case
// never judged the credentials, so it stays the per-service checks' business
// to report, as it was before this check existed.
func checkOAuthCredentials(ctx context.Context, api tailscaleAPI) error {
	err := api.Authenticate(ctx)
	if err == nil || errors.Is(err, ErrOAuthTokenEndpointUnavailable) {
		return nil
	}
	return err
}

// renderServiceState turns the two verification sources into one display
// state. Advertisement is the local daemon's fact, approval the cloud's: an
// unadvertised service is unreachable no matter what the tailnet thinks, and
// a pending one needs a human (or an autoApprovers rule), so each gets its
// own actionable message.
func renderServiceState(advertised bool, approval approvalState, cloudChecked bool, tag, hint string) string {
	switch {
	case !advertised:
		return fmt.Sprintf("not advertised (check %s)", hint)
	case !cloudChecked:
		return "advertised"
	case approval == serviceApproved:
		return "approved"
	case approval == servicePending:
		return fmt.Sprintf("pending approval (approve it in the Tailscale admin console, or add an autoApprovers.services rule for %s)", tag)
	default:
		return fmt.Sprintf("advertised, but not yet in the tailnet (check %s)", hint)
	}
}
