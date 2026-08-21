package docker

import (
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	// The call site passes the resolved accessor, and a zero Profile resolves
	// to Once's own default - so the flag can never come through empty and be
	// dropped, which is what would let verifyHTTP probe the wrong path.
	t.Run("with a zero Profile's resolved path", func(t *testing.T) {
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

func TestPinnedImage(t *testing.T) {
	proxy := NewProxy(&Namespace{name: "once"})
	assert.Equal(t, proxyImage, proxy.PinnedImage())

	proxy.OverrideImageForTest("basecamp/kamal-proxy:older")
	assert.Equal(t, "basecamp/kamal-proxy:older", proxy.PinnedImage())

	proxy.OverrideImageForTest("")
	assert.Equal(t, proxyImage, proxy.PinnedImage())
}

func TestRollSettings(t *testing.T) {
	inspect := func(label string) container.InspectResponse {
		labels := map[string]string{}
		if label != "" {
			labels[labelKey] = label
		}
		return container.InspectResponse{
			Config: &container.Config{Image: "basecamp/kamal-proxy:older", Labels: labels},
		}
	}
	fallback := ProxySettings{HTTPPort: 80, HTTPSPort: 443, MetricsPort: 1318}

	t.Run("prefers the container's label over the fallback", func(t *testing.T) {
		labelled := ProxySettings{HTTPPort: 8080, HTTPSPort: 8443, MetricsPort: 9000}

		settings, err := rollSettings(inspect(labelled.Marshal()), fallback)

		require.NoError(t, err)
		assert.Equal(t, labelled, settings)
	})

	t.Run("falls back without a label", func(t *testing.T) {
		settings, err := rollSettings(inspect(""), fallback)

		require.NoError(t, err)
		assert.Equal(t, fallback, settings)
	})

	t.Run("fails on a corrupt label", func(t *testing.T) {
		_, err := rollSettings(inspect("{not json"), fallback)

		assert.ErrorContains(t, err, "unmarshalling proxy settings")
	})
}
