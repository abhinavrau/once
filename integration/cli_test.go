package integration

import (
	"context"
	"encoding/json"
	"io"
	"os"
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

	// Deploying by catalog name resolves to the app's image ref, and a deploy
	// with no flags is local: host derived from the app name with the
	// .localhost suffix, TLS off, and the exposure stored in the label.
	require.NoError(t, runCLI("-n", nsName, "deploy", "campfire"))

	restored, err := docker.RestoreNamespace(ctx, nsName)
	require.NoError(t, err)
	app := restored.ApplicationByHost("campfire.localhost")
	require.NotNil(t, app, "expected the host derived from the catalog entry BaseName")
	assert.Equal(t, "ghcr.io/basecamp/once-campfire", app.Settings.Image)
	assert.Equal(t, docker.ExposureLocal, app.Settings.Exposure)
	assert.False(t, app.Settings.TLSEnabled())
	appName := app.Settings.Name
	assertAppLabels(t, ctx, cli, restored, appName, func(t *testing.T, labels map[string]string) {
		assert.Contains(t, labels["once"], `"exposure":"local"`)
	})

	// The read commands emit parseable JSON under --json: list an array of
	// apps, per-app status one object, tailscale status the enablement state.
	var listed []map[string]any
	unmarshalCLI(t, &listed, "-n", nsName, "list", "--json")
	require.Len(t, listed, 1)
	assert.Equal(t, appName, listed[0]["name"])
	assert.Equal(t, "campfire.localhost", listed[0]["host"])
	assert.Equal(t, "local", listed[0]["exposure"])
	assert.Equal(t, true, listed[0]["running"])

	var status map[string]any
	unmarshalCLI(t, &status, "-n", nsName, "status", "campfire.localhost", "--json")
	assert.Equal(t, appName, status["name"])
	assert.Equal(t, "local", status["exposure"])

	var ts map[string]any
	unmarshalCLI(t, &ts, "-n", nsName, "tailscale", "status", "--json")
	assert.Equal(t, false, ts["enabled"])

	// A bare --host under local exposure gets the suffix appended, and the
	// stored exposure survives the update's container recreation.
	require.NoError(t, runCLI("-n", nsName, "update", "campfire.localhost", "--host", "writebook"))

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

	// Catalog names compose with deploy flags like --expose.
	err := runCLI("-n", nsName, "deploy", "campfire", "--expose", "public")
	assert.ErrorIs(t, err, docker.ErrPublicHostRequired)

	err = runCLI("-n", nsName, "deploy", "ghcr.io/basecamp/once-campfire:main", "--expose", "public", "--host", "writebook")
	assert.ErrorIs(t, err, docker.ErrPublicHostRequired)
}

// TestCLIDeployTailnetRequiresTailscaleEnable proves a tailnet deploy before
// `once tailscale enable` fails fast with the pointer to the fix and leaves
// nothing behind.
func TestCLIDeployTailnetRequiresTailscaleEnable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	nsName := "once-cli-tailnet-test"
	ns, err := docker.NewNamespace(nsName)
	require.NoError(t, err)
	t.Cleanup(func() { ns.Teardown(context.Background(), true) })

	require.NoError(t, ns.EnsureNetwork(ctx))
	require.NoError(t, ns.Proxy().Boot(ctx, getProxyPorts(t)))

	err = runCLI("-n", nsName, "deploy", "ghcr.io/basecamp/once-campfire:main", "--expose", "tailnet")
	require.ErrorIs(t, err, docker.ErrTailscaleNotEnabled)
	assert.ErrorContains(t, err, "once tailscale enable")

	restored, err := docker.RestoreNamespace(ctx, nsName)
	require.NoError(t, err)
	assert.Empty(t, restored.Applications(), "a refused tailnet deploy must leave no app behind")
}

func TestCLIRemovedFlags(t *testing.T) {
	err := runCLI("-n", "once-cli-removed-test", "deploy", "ghcr.io/basecamp/once-campfire:main", "--tailscale")
	assert.ErrorContains(t, err, "unknown flag: --tailscale")

	err = runCLI("-n", "once-cli-removed-test", "update", "app.localhost", "--backup-path", "/backups")
	assert.ErrorContains(t, err, "once backup enable")
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

// captureCLIOutput runs a CLI command capturing what it prints to os.Stdout,
// returning the output alongside the command's error.
func captureCLIOutput(args ...string) (string, error) {
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	// Drain concurrently so output larger than the pipe buffer can't deadlock.
	outCh := make(chan []byte, 1)
	go func() {
		out, _ := io.ReadAll(r)
		outCh <- out
	}()

	runErr := runCLI(args...)
	w.Close()
	return string(<-outCh), runErr
}

// unmarshalCLI runs a CLI command capturing os.Stdout and unmarshals the
// output, proving it is valid JSON as any consumer like jq would see it.
func unmarshalCLI(t *testing.T, v any, args ...string) {
	t.Helper()

	out, err := captureCLIOutput(args...)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(out), v), "output was: %s", out)
}
