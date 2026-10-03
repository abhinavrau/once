package integration

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
)

// TestRemoveStrayContainersAfterKilledDeploys leaves behind the App
// Containers killed Deploys leave: first one that never became healthy and
// restart-loops while the Proxy still routes to the original, then a healthy
// one the Proxy had already been switched to. Each time the namespace
// reports the container the Proxy routes to, and the sweep - once the
// containers have settled - removes the other while the routed one keeps
// serving.
func TestRemoveStrayContainersAfterKilledDeploys(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ns, err := docker.NewNamespace("once-stray-test")
	require.NoError(t, err)
	defer ns.Teardown(ctx, true)

	require.NoError(t, ns.EnsureNetwork(ctx))
	ports := getProxyPorts(t)
	bootProxy(t, ctx, ns, ports)

	app := deployApp(t, ctx, ns, docker.ApplicationSettings{
		Name:     "whoami",
		Image:    whoamiImage,
		Host:     "whoami.localhost",
		Exposure: docker.ExposureLocal,
	})
	originalName, err := app.ContainerName(ctx)
	require.NoError(t, err)

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	defer cli.Close()

	original, err := cli.ContainerInspect(ctx, originalName)
	require.NoError(t, err)

	const prefix = "once-stray-test-app-whoami-"
	createSibling := func(suffix string, cmd []string) container.InspectResponse {
		t.Helper()
		config := *original.Config
		config.Hostname = ""
		config.Cmd = cmd
		resp, err := cli.ContainerCreate(ctx, &config, original.HostConfig,
			&network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{ns.Name(): {}}},
			nil, prefix+suffix)
		require.NoError(t, err)
		require.NoError(t, cli.ContainerStart(ctx, resp.ID, container.StartOptions{}))
		info, err := cli.ContainerInspect(ctx, resp.ID)
		require.NoError(t, err)
		return info
	}
	servedBy := func() string {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/", ports.HTTPPort), nil)
		require.NoError(t, err)
		req.Host = "whoami.localhost"
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return string(body)
	}
	assertActive := func(want container.InspectResponse) *docker.Namespace {
		t.Helper()
		restored, err := docker.RestoreNamespace(ctx, ns.Name())
		require.NoError(t, err)
		restoredApp := restored.Application("whoami")
		require.NotNil(t, restoredApp)
		assert.True(t, restoredApp.Running)
		name, err := restoredApp.ContainerName(ctx)
		require.NoError(t, err)
		assert.Equal(t, strings.TrimPrefix(want.Name, "/"), name)
		return restored
	}
	sweepOnceSettled := func(restored *docker.Namespace) {
		t.Helper()
		require.NoError(t, restored.RemoveStrayContainers(ctx, time.Now()))
		assert.Equal(t, 2, countContainers(t, ctx, prefix), "a fresh sibling may be a Deploy still in flight")
		require.NoError(t, restored.RemoveStrayContainers(ctx, time.Now().Add(time.Hour)))
		assert.Equal(t, 1, countContainers(t, ctx, prefix))
	}

	// Killed before the Proxy switch: the new container never got healthy.
	looping := createSibling("aaaaa1", []string{"--port", "not-a-port"})
	require.Eventually(t, func() bool {
		info, err := cli.ContainerInspect(ctx, looping.ID)
		return err == nil && info.State.Restarting
	}, 30*time.Second, 500*time.Millisecond, "the never-healthy sibling restart-loops")

	sweepOnceSettled(assertActive(original))
	assertContainerRunning(t, ctx, originalName, true)
	assert.Contains(t, servedBy(), "X-Kamal-Target: "+original.ID[:12])

	// Killed after the Proxy switch, before the old container was removed.
	switched := createSibling("bbbbb2", nil)
	require.NoError(t, ns.Proxy().Deploy(ctx, docker.DeployOptions{
		AppName:         "whoami",
		Target:          switched.ID[:12],
		Host:            "whoami.localhost",
		HealthCheckPath: docker.DefaultHealthCheckPath,
	}))

	sweepOnceSettled(assertActive(switched))
	assertContainerRunning(t, ctx, strings.TrimPrefix(switched.Name, "/"), true)
	assert.Contains(t, servedBy(), "X-Kamal-Target: "+switched.ID[:12])
}
