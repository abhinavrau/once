package docker

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPublicDNSWarning(t *testing.T) {
	stub := func(addrs []string, err error) {
		orig := lookupHost
		lookupHost = func(ctx context.Context, host string) ([]string, error) {
			return addrs, err
		}
		t.Cleanup(func() { lookupHost = orig })
	}

	t.Run("unresolvable host warns that TLS will fail", func(t *testing.T) {
		stub(nil, errors.New("no such host"))
		msg := PublicDNSWarning(context.Background(), ExposurePublic, "app.example.com")
		assert.Contains(t, msg, "could not resolve app.example.com (no such host)")
		assert.Contains(t, msg, "TLS certificate issuance will fail")
	})

	t.Run("resolvable host echoes the resolved address", func(t *testing.T) {
		stub([]string{"192.0.2.1", "2001:db8::1"}, nil)
		msg := PublicDNSWarning(context.Background(), ExposurePublic, "app.example.com")
		assert.Contains(t, msg, "app.example.com resolves to 192.0.2.1, 2001:db8::1")
	})

	t.Run("non-public exposures skip the lookup entirely", func(t *testing.T) {
		orig := lookupHost
		lookupHost = func(ctx context.Context, host string) ([]string, error) {
			t.Fatal("resolver called for non-public exposure")
			return nil, nil
		}
		t.Cleanup(func() { lookupHost = orig })

		assert.Empty(t, PublicDNSWarning(context.Background(), ExposureLocal, "app.localhost"))
		assert.Empty(t, PublicDNSWarning(context.Background(), ExposureTailnet, "app"))
	})
}
