package docker

import (
	"context"
	"fmt"
	"net/http"
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
	settings, err := t.LoadSettings(ctx)
	if err != nil {
		return nil, err
	}
	return serviceStatuses(ctx, t.daemonFor(settings.Mode), newTailscaleAPI(settings), settings, names)
}

// Helpers

func serviceStatuses(ctx context.Context, daemon TailscaleDaemon, api tailscaleAPI, settings TailscaleSettings, names []string) ([]ServiceStatus, error) {
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
			State: renderServiceState(advertised, approval, cloudChecked, settings.Tag),
		})
	}
	return statuses, nil
}

// checkOAuthCredentials proves the OAuth credentials before any service state
// is reported. The adapter exchanges them lazily on its first call (ADR-0011),
// so without a call of its own a revoked credential is only noticed wherever
// the per-service cloud check happens to run - never, when nothing is
// advertised, and mid-listing when only some services are. Only a rejected
// credential is fatal here: a tailnet that is merely unwell stays the
// per-service checks' business to report, as it was before this check existed.
func checkOAuthCredentials(ctx context.Context, api tailscaleAPI) error {
	status, body, err := api.Call(ctx, http.MethodGet, "/tailnet/-/devices", nil)
	if err != nil {
		return err
	}
	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("Tailscale rejected the OAuth credentials (HTTP %d): %s; check the client ID and secret are correct and not revoked", status, errorMessage(body))
	case http.StatusForbidden:
		return fmt.Errorf("the OAuth client is missing the devices scope (Devices -> Core), needed to read which hosts advertise each Service (HTTP %d): %s", status, errorMessage(body))
	}
	return nil
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
