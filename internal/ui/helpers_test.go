package ui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/once/internal/docker"
)

func keyPressMsg(s string) tea.KeyPressMsg {
	k := tea.Key{Text: s}
	if r := []rune(s); len(r) == 1 {
		k.Code = r[0]
	}
	return tea.KeyPressMsg(k)
}

func stubLookupHost(t *testing.T, addrs []string, err error) {
	t.Helper()

	swapLookupHost(t, func(context.Context, string) ([]string, error) {
		return addrs, err
	})
}

func failOnLookup(t *testing.T) {
	t.Helper()

	swapLookupHost(t, func(_ context.Context, host string) ([]string, error) {
		t.Errorf("resolver called for %q", host)
		return nil, nil
	})
}

func swapLookupHost(t *testing.T, fn func(context.Context, string) ([]string, error)) {
	orig := docker.LookupHost
	docker.LookupHost = fn
	t.Cleanup(func() { docker.LookupHost = orig })
}

// stubHostTailnetSuffix pins the machine's own tailnet as seen by the DNS
// warning, so a failed-lookup test never consults the real host tailscaled.
func stubHostTailnetSuffix(t *testing.T, suffix string, onTailnet bool) {
	t.Helper()

	orig := docker.HostTailnetSuffix
	docker.HostTailnetSuffix = func(context.Context) (string, bool) {
		return suffix, onTailnet
	}
	t.Cleanup(func() { docker.HostTailnetSuffix = orig })
}
