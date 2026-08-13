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
