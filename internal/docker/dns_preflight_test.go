package docker

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPublicDNSWarning(t *testing.T) {
	t.Run("unresolvable host warns that TLS will fail", func(t *testing.T) {
		stubLookupHost(t, nil, errors.New("no such host"))
		msg := PublicDNSWarning(context.Background(), ExposurePublic, "app.example.com")
		assert.Contains(t, msg, "could not resolve app.example.com (no such host)")
		assert.Contains(t, msg, "TLS certificate issuance will fail")
	})

	t.Run("resolvable host echoes the resolved address", func(t *testing.T) {
		stubLookupHost(t, []string{"192.0.2.1", "2001:db8::1"}, nil)
		msg := PublicDNSWarning(context.Background(), ExposurePublic, "app.example.com")
		assert.Contains(t, msg, "app.example.com resolves to 192.0.2.1, 2001:db8::1")
	})

	t.Run("non-public exposures skip the lookup entirely", func(t *testing.T) {
		failOnLookup(t)

		assert.Empty(t, PublicDNSWarning(context.Background(), ExposureLocal, "app.localhost"))
		assert.Empty(t, PublicDNSWarning(context.Background(), ExposureTailnet, "app"))
	})
}

func TestTailnetDNSWarning(t *testing.T) {
	const serviceURL = "https://writebook.tail1234.ts.net"

	t.Run("resolvable service name warns about nothing", func(t *testing.T) {
		stubLookupHost(t, []string{"100.64.0.1"}, nil)
		assert.Empty(t, TailnetDNSWarning(context.Background(), serviceURL))
	})

	t.Run("unresolvable on the same tailnet warns about MagicDNS without CLI advice", func(t *testing.T) {
		stubLookupHost(t, nil, errors.New("server misbehaving"))
		stubHostTailnetSuffix(t, "tail1234.ts.net", true)
		msg := TailnetDNSWarning(context.Background(), serviceURL)
		assert.Contains(t, msg, "writebook.tail1234.ts.net does not resolve from this machine")
		assert.Contains(t, msg, "may still be registering")
		assert.Contains(t, msg, "MagicDNS may be broken")
		assert.Contains(t, msg, "Other tailnet devices may be unaffected")
		assert.NotContains(t, msg, "tailscale set", "the warning must not tell the user to run the tailscale CLI")
	})

	t.Run("an unknown host suffix keeps the conditional warning", func(t *testing.T) {
		stubLookupHost(t, nil, errors.New("server misbehaving"))
		stubHostTailnetSuffix(t, "", true)
		msg := TailnetDNSWarning(context.Background(), serviceURL)
		assert.Contains(t, msg, "does not resolve from this machine")
		assert.Contains(t, msg, "if this machine is on the tailnet")
	})

	t.Run("a machine on another tailnet names the mismatch instead of MagicDNS advice", func(t *testing.T) {
		stubLookupHost(t, nil, errors.New("no such host"))
		stubHostTailnetSuffix(t, "other5678.ts.net", true)
		msg := TailnetDNSWarning(context.Background(), serviceURL)
		assert.Contains(t, msg, "this machine is on tailnet other5678.ts.net, not tail1234.ts.net")
		assert.Contains(t, msg, "device on tail1234.ts.net")
		assert.NotContains(t, msg, "accept-dns")
		assert.NotContains(t, msg, "may still be registering")
	})

	t.Run("a machine on no tailnet says so instead of MagicDNS advice", func(t *testing.T) {
		stubLookupHost(t, nil, errors.New("no such host"))
		stubHostTailnetSuffix(t, "", false)
		msg := TailnetDNSWarning(context.Background(), serviceURL)
		assert.Contains(t, msg, "this machine is not on a tailnet")
		assert.Contains(t, msg, "device on tail1234.ts.net")
		assert.NotContains(t, msg, "accept-dns")
	})

	t.Run("an empty answer counts as unresolvable", func(t *testing.T) {
		stubLookupHost(t, nil, nil)
		stubHostTailnetSuffix(t, "tail1234.ts.net", true)
		assert.Contains(t, TailnetDNSWarning(context.Background(), serviceURL), "does not resolve from this machine")
	})

	t.Run("a missing URL skips the lookup", func(t *testing.T) {
		failOnLookup(t)
		assert.Empty(t, TailnetDNSWarning(context.Background(), ""))
	})

	t.Run("a resolvable name never consults the host tailscaled", func(t *testing.T) {
		stubLookupHost(t, []string{"100.64.0.1"}, nil)
		swapHostTailnetSuffix(t, func(context.Context) (string, bool) {
			t.Error("host tailscaled consulted for a name that resolves")
			return "", false
		})
		assert.Empty(t, TailnetDNSWarning(context.Background(), serviceURL))
	})
}

// Helpers

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
	orig := LookupHost
	LookupHost = fn
	t.Cleanup(func() { LookupHost = orig })
}

func stubHostTailnetSuffix(t *testing.T, suffix string, onTailnet bool) {
	t.Helper()

	swapHostTailnetSuffix(t, func(context.Context) (string, bool) {
		return suffix, onTailnet
	})
}

func swapHostTailnetSuffix(t *testing.T, fn func(context.Context) (string, bool)) {
	orig := HostTailnetSuffix
	HostTailnetSuffix = fn
	t.Cleanup(func() { HostTailnetSuffix = orig })
}
