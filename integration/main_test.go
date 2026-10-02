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
		ports, err := freeProxyPorts()
		if err != nil {
			panic(err)
		}
		return ports
	})
	os.Exit(m.Run())
}
