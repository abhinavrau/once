package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

// serviceVerify bounds the post-enable wait for every expected service to be
// advertised by the local tailscaled and approved in the tailnet. Docktail
// reconciles labels within seconds; the bound covers slow cloud propagation.
const (
	serviceVerifyTimeout = 90 * time.Second
	serviceVerifyPoll    = 2 * time.Second
)

// VerifyServices confirms each expected service is reachable-in-principle
// after enable: advertised according to the local daemon's serve status, and
// approved according to the Tailscale cloud API - authoritative, since local
// state can lag (tailscale/tailscale#18821). Skipped entirely on the
// headscale control seam: Services do not exist on headscale - the cloud
// endpoints are Tailscale SaaS only, and even local advertisement cannot
// succeed because `tailscale serve --service --https` needs the control
// plane's HTTPS cert machinery, which headscale lacks.
func (t *Tailscale) VerifyServices(ctx context.Context, expected []string, progress DeployProgressCallback) error {
	if len(expected) == 0 || controlSeamActive() {
		return nil
	}

	settings, err := t.LoadSettings(ctx)
	if err != nil {
		return err
	}

	announce := func(line string) {
		if progress != nil {
			progress(DeployProgress{Stage: DeployStageWaiting, Status: line})
		}
	}
	if err := waitForAdvertised(ctx, t.daemonFor(settings.Mode), expected, serviceVerifyTimeout, serviceVerifyPoll, announce); err != nil {
		return err
	}
	return waitForApproved(ctx, newTailscaleAPI(settings), settings, expected, serviceVerifyTimeout, serviceVerifyPoll, announce)
}

// Helpers

// waitForAdvertised polls the local daemon's serve status until every expected
// service is advertised, or fails at timeout: with the daemon's error if it
// couldn't be read, else naming the services still missing.
func waitForAdvertised(ctx context.Context, daemon TailscaleDaemon, expected []string, timeout, poll time.Duration, announce func(string)) error {
	if announce != nil {
		announce(fmt.Sprintf("waiting up to %s for the local tailscaled to advertise: %s", timeout, strings.Join(expected, ", ")))
	}
	deadline := time.Now().Add(timeout)
	missing := expected
	for {
		status, err := daemon.ServeStatus(ctx)
		if err == nil {
			missing = missingServices(status.Services, expected)
			if len(missing) == 0 {
				return nil
			}
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("reading serve status from the local tailscaled: %w", err)
			}
			return fmt.Errorf("not yet advertised by the local tailscaled: %s; check the once-docktail container logs", strings.Join(missing, ", "))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

// waitForApproved polls the cloud API's service-hosts endpoint (the same one
// Docktail uses) until every expected service has an approved advertising
// host. A host stuck at not-approved fails immediately: approval needs a
// human or an autoApprovers rule, so waiting cannot fix it.
func waitForApproved(ctx context.Context, api tailscaleAPI, settings TailscaleSettings, expected []string, timeout, poll time.Duration, announce func(string)) error {
	if announce != nil {
		announce(fmt.Sprintf("waiting up to %s for the tailnet to approve: %s", timeout, strings.Join(expected, ", ")))
	}

	deadline := time.Now().Add(timeout)
	unapproved := expected
	for {
		var waiting []string
		for _, name := range unapproved {
			state, err := serviceApprovalState(ctx, api, name)
			if err != nil {
				return err
			}
			switch state {
			case serviceApproved:
			case servicePending:
				return fmt.Errorf("service %q is advertised but pending approval in the tailnet; approve it in the Tailscale admin console (Services), or add an autoApprovers.services rule for %s so services are approved automatically", name, settings.Tag)
			default:
				waiting = append(waiting, name)
			}
		}
		unapproved = waiting
		if len(unapproved) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("never appeared in the tailnet: %s; check the once-docktail container logs", strings.Join(unapproved, ", "))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

type approvalState int

const (
	serviceUnknown approvalState = iota // not (yet) known to the control plane
	servicePending                      // advertised, but no approved host
	serviceApproved
)

// serviceApprovalState reads which devices advertise svc:<name> and whether
// any is approved, via GET /tailnet/-/services/svc:<name>/devices - the same
// request Docktail's listServiceHosts sends. approvalLevel is one of
// not-approved, approved:auto, approved:manual.
func serviceApprovalState(ctx context.Context, api tailscaleAPI, name string) (approvalState, error) {
	status, body, err := api.Call(ctx, http.MethodGet, "/tailnet/-/services/svc:"+name+"/devices", nil)
	if err != nil {
		return serviceUnknown, err
	}
	switch {
	case status == http.StatusNotFound:
		// Docktail hasn't created the Service definition yet.
		return serviceUnknown, nil
	case status != http.StatusOK:
		return serviceUnknown, fmt.Errorf("listing devices hosting service %q (HTTP %d): %s", name, status, errorMessage(body))
	}

	var result struct {
		Hosts []struct {
			ApprovalLevel string `json:"approvalLevel"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return serviceUnknown, fmt.Errorf("decoding service %q hosts: %w", name, err)
	}
	if len(result.Hosts) == 0 {
		return serviceUnknown, nil
	}
	for _, host := range result.Hosts {
		if strings.HasPrefix(host.ApprovalLevel, "approved") {
			return serviceApproved, nil
		}
	}
	return servicePending, nil
}

// missingServices returns the expected service names absent from advertised.
func missingServices(advertised, expected []string) []string {
	var missing []string
	for _, name := range expected {
		if !slices.Contains(advertised, name) {
			missing = append(missing, name)
		}
	}
	return missing
}
