package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProxyContainerName(t *testing.T) {
	ns := &Namespace{name: "once"}
	proxy := NewProxy(ns)
	assert.Equal(t, "once-proxy", proxy.containerName())

	ns2 := &Namespace{name: "staging"}
	proxy2 := NewProxy(ns2)
	assert.Equal(t, "staging-proxy", proxy2.containerName())
}

func TestDeployArgs(t *testing.T) {
	proxy := &Proxy{}

	t.Run("basic deploy includes timeout", func(t *testing.T) {
		args := proxy.deployArgs(DeployOptions{AppName: "chat", Target: "localhost:3000"})

		assert.Equal(t, []string{
			"kamal-proxy", "deploy", "chat",
			"--target", "localhost:3000",
			"--deploy-timeout", "120s",
		}, args)
	})

	t.Run("with health check path", func(t *testing.T) {
		args := proxy.deployArgs(DeployOptions{AppName: "vault", Target: "localhost:3000", HealthCheckPath: "/alive"})

		assert.Contains(t, args, "--health-check-path")
		assert.Contains(t, args, "/alive")
	})

	// Every deploy names a path, because the call site passes the resolved
	// accessor and a zero Profile resolves to Once's own default. kamal-proxy's
	// default is that same value, so the flag is a no-op for a Rails app - what
	// matters is that it can never come through empty and be dropped.
	t.Run("a zero Profile still names a path", func(t *testing.T) {
		args := proxy.deployArgs(DeployOptions{
			AppName:         "chat",
			Target:          "localhost:3000",
			HealthCheckPath: AppProfile{}.HealthCheckPath(),
		})

		assert.Contains(t, args, "--health-check-path")
		assert.Contains(t, args, "/up")
	})

	t.Run("with host", func(t *testing.T) {
		args := proxy.deployArgs(DeployOptions{AppName: "chat", Target: "localhost:3000", Host: "chat.example.com"})

		assert.Contains(t, args, "--host")
		assert.Contains(t, args, "chat.example.com")
	})

	t.Run("with TLS", func(t *testing.T) {
		args := proxy.deployArgs(DeployOptions{AppName: "chat", Target: "localhost:3000", TLS: true})

		assert.Contains(t, args, "--tls")
	})

	t.Run("with host and TLS", func(t *testing.T) {
		args := proxy.deployArgs(DeployOptions{
			AppName: "chat",
			Target:  "localhost:3000",
			Host:    "chat.example.com",
			TLS:     true,
		})

		assert.Equal(t, []string{
			"kamal-proxy", "deploy", "chat",
			"--target", "localhost:3000",
			"--deploy-timeout", "120s",
			"--host", "chat.example.com",
			"--tls",
		}, args)
	})
}
