package ui

import (
	"context"
	"sync"

	"github.com/basecamp/once/internal/docker"
	"github.com/basecamp/once/internal/handoff"
)

// PageKit is the Recovery Kit as the phone page shows it: the password in a
// field of its own, and the rest as the details a fresh host needs beside it.
// The password is dropped from the details because the page puts it where a
// password manager will find it, which is the whole point of serving a page.
func (k RecoveryKit) PageKit() handoff.Kit {
	page := handoff.Kit{Password: k.Password, Hostname: k.Hostname}
	for _, row := range k.rows() {
		if row.label == "Password" {
			continue
		}
		page.Details = append(page.Details, handoff.Detail{Label: row.label, Value: row.value})
	}
	return page
}

// NewNamespaceCustodyHandoff publishes the Recovery Kit through the install's
// own Proxy and daemon. It is the only place that knows the URL on the custody
// screen comes from Docker at all.
func NewNamespaceCustodyHandoff(namespace *docker.Namespace) CustodyHandoff {
	return &namespaceHandoff{namespace: namespace}
}

type namespaceHandoff struct {
	namespace *docker.Namespace
	// Start and Stop are dispatched as separate commands, so they can run on
	// different goroutines.
	mutex sync.Mutex
	live  *docker.RecoveryHandoff
}

func (h *namespaceHandoff) Start(ctx context.Context, kit RecoveryKit) (string, error) {
	live, err := h.namespace.StartRecoveryHandoff(ctx, kit.PageKit())
	if err != nil {
		return "", err
	}

	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.live = live
	return live.URL(), nil
}

// Stop is idempotent, because it is called from wherever the flow ended as
// well as by the host tearing down: a route left standing outlives the screen.
func (h *namespaceHandoff) Stop(ctx context.Context) error {
	h.mutex.Lock()
	live := h.live
	h.live = nil
	h.mutex.Unlock()

	if live == nil {
		return nil
	}
	return live.Close(ctx)
}
