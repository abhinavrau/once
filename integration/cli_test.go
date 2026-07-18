package integration

import (
	"context"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/command"
	"github.com/basecamp/once/internal/docker"
)

func TestCLIDeployExposureLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	nsName := "once-cli-exposure-test"
	ns, err := docker.NewNamespace(nsName)
	require.NoError(t, err)
	// The CLI deploys through its own namespace instance, so restore ours
	// before teardown or it won't know about the app containers to remove.
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if restored, err := docker.RestoreNamespace(cleanupCtx, nsName); err == nil {
			restored.Teardown(cleanupCtx, true)
		}
	})

	require.NoError(t, ns.EnsureNetwork(ctx))
	require.NoError(t, ns.Proxy().Boot(ctx, getProxyPorts(t)))

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	t.Cleanup(func() { cli.Close() })

	// A deploy with no flags is local: host derived from the app name with
	// the .localhost suffix, TLS off, and the exposure stored in the label.
	require.NoError(t, runCLI("-n", nsName, "deploy", "ghcr.io/basecamp/once-campfire:main"))

	restored, err := docker.RestoreNamespace(ctx, nsName)
	require.NoError(t, err)
	app := restored.ApplicationByHost("once-campfire.localhost")
	require.NotNil(t, app, "expected the app at the name-derived .localhost host")
	assert.Equal(t, docker.ExposureLocal, app.Settings.Exposure)
	assert.False(t, app.Settings.TLSEnabled())
	appName := app.Settings.Name
	assertAppLabels(t, ctx, cli, restored, appName, func(t *testing.T, labels map[string]string) {
		assert.Contains(t, labels["once"], `"exposure":"local"`)
	})

	// A bare --host under local exposure gets the suffix appended, and the
	// stored exposure survives the update's container recreation.
	require.NoError(t, runCLI("-n", nsName, "update", "once-campfire.localhost", "--host", "writebook"))

	restored, err = docker.RestoreNamespace(ctx, nsName)
	require.NoError(t, err)
	app = restored.ApplicationByHost("writebook.localhost")
	require.NotNil(t, app, "expected the bare host to gain the .localhost suffix")
	assert.Equal(t, docker.ExposureLocal, app.Settings.Exposure)
	assertAppLabels(t, ctx, cli, restored, appName, func(t *testing.T, labels map[string]string) {
		assert.Contains(t, labels["once"], `"exposure":"local"`)
	})

	// Promoting to public without a real FQDN fails fast without touching the app.
	err = runCLI("-n", nsName, "update", "writebook.localhost", "--expose", "public")
	assert.ErrorIs(t, err, docker.ErrPublicHostRequired)
}

func TestCLIDeployPublicRequiresFQDN(t *testing.T) {
	nsName := "once-cli-public-test"

	err := runCLI("-n", nsName, "deploy", "ghcr.io/basecamp/once-campfire:main", "--expose", "public")
	assert.ErrorIs(t, err, docker.ErrPublicHostRequired)

	err = runCLI("-n", nsName, "deploy", "ghcr.io/basecamp/once-campfire:main", "--expose", "public", "--host", "writebook")
	assert.ErrorIs(t, err, docker.ErrPublicHostRequired)
}

func TestCLIRemovedTailscaleFlagNamesReplacement(t *testing.T) {
	for _, cmd := range [][]string{
		{"deploy", "ghcr.io/basecamp/once-campfire:main", "--tailscale"},
		{"update", "app.localhost", "--tailscale=false"},
	} {
		err := runCLI(append([]string{"-n", "once-cli-removed-test"}, cmd...)...)
		assert.ErrorContains(t, err, "--expose tailnet")
	}
}

// TestCLIBackfillsExposureForLegacyApps plants containers whose once labels
// predate the exposure field and asserts loading the namespace backfills the
// exposure from the hostname shape.
func TestCLIBackfillsExposureForLegacyApps(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	nsName := "once-cli-backfill-test"

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	t.Cleanup(func() { cli.Close() })

	pullImage(t, ctx, cli, whoamiImage)

	createLegacyApp := func(appName, label string) {
		resp, err := cli.ContainerCreate(ctx, &container.Config{
			Image:  whoamiImage,
			Labels: map[string]string{"once": label},
		}, nil, nil, nil, nsName+"-app-"+appName+"-abc123")
		require.NoError(t, err)
		t.Cleanup(func() {
			cli.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{Force: true})
		})
	}

	createLegacyApp("chat", `{"name":"chat","image":"img:1","host":"chat.localhost"}`)
	createLegacyApp("blog", `{"name":"blog","image":"img:1","host":"blog.example.com"}`)

	ns, err := docker.RestoreNamespace(ctx, nsName)
	require.NoError(t, err)

	chat := ns.Application("chat")
	require.NotNil(t, chat)
	assert.Equal(t, docker.ExposureLocal, chat.Settings.Exposure)

	blog := ns.Application("blog")
	require.NotNil(t, blog)
	assert.Equal(t, docker.ExposurePublic, blog.Settings.Exposure)
	assert.True(t, blog.Settings.TLSEnabled())
}

// Helpers

func runCLI(args ...string) error {
	return command.NewRootCommand().ExecuteArgs(args...)
}
