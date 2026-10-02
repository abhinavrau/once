package integration

import (
	"os"
	"testing"

	"github.com/basecamp/once/internal/docker"
)

// TestMain moves every proxy booted without explicit ports - a fresh
// install's Setup, wherever it is reached from - off :80/:443/:1318, which a
// live install on the same host holds.
func TestMain(m *testing.M) {
	docker.OverrideDefaultPortsForTest(func() docker.ProxySettings {
		return docker.ProxySettings{
			HTTPPort:    mustFreePort(),
			HTTPSPort:   mustFreePort(),
			MetricsPort: mustFreePort(),
		}
	})
	os.Exit(m.Run())
}

// Helpers

func mustFreePort() int {
	port, err := nextFreePort()
	if err != nil {
		panic(err)
	}
	return port
}
