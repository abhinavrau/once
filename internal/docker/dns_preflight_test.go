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

	runningApp := func() *Application {
		return &Application{
			Settings: ApplicationSettings{Name: "writebook", Host: "writebook", Exposure: ExposureTailnet},
			Running:  true,
		}
	}

	t.Run("resolvable service name warns about nothing", func(t *testing.T) {
		stubLookupHost(t, []string{"100.64.0.1"}, nil)
		assert.Empty(t, TailnetDNSWarning(context.Background(), runningApp(), serviceURL))
	})

	t.Run("unresolvable service name warns conditionally with the accept-dns hint", func(t *testing.T) {
		stubLookupHost(t, nil, errors.New("server misbehaving"))
		msg := TailnetDNSWarning(context.Background(), runningApp(), serviceURL)
		assert.Contains(t, msg, "writebook.tail1234.ts.net does not resolve from this machine")
		assert.Contains(t, msg, "may still be registering")
		assert.Contains(t, msg, "if this machine is on the tailnet")
		assert.Contains(t, msg, "tailscale set --accept-dns=false")
		assert.Contains(t, msg, "tailscale set --accept-dns=true")
		assert.Contains(t, msg, "Other tailnet devices may be unaffected")
	})

	t.Run("an empty answer counts as unresolvable", func(t *testing.T) {
		stubLookupHost(t, nil, nil)
		assert.Contains(t, TailnetDNSWarning(context.Background(), runningApp(), serviceURL), "does not resolve from this machine")
	})

	t.Run("a stopped app skips the lookup", func(t *testing.T) {
		failOnLookup(t)

		app := runningApp()
		app.Running = false
		assert.Empty(t, TailnetDNSWarning(context.Background(), app, serviceURL))
	})

	t.Run("non-tailnet apps and missing URLs skip the lookup", func(t *testing.T) {
		failOnLookup(t)

		assert.Empty(t, TailnetDNSWarning(context.Background(), runningApp(), ""))

		local := runningApp()
		local.Settings.Exposure = ExposureLocal
		assert.Empty(t, TailnetDNSWarning(context.Background(), local, serviceURL))
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
	orig := lookupHost
	lookupHost = fn
	t.Cleanup(func() { lookupHost = orig })
}
