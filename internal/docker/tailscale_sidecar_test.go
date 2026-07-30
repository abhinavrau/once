package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSidecarBootClient answers the boot dispatch's inspect with a canned
// container state and records the engine calls it makes, in order.
type fakeSidecarBootClient struct {
	state      *container.State
	inspectErr error
	removeErr  error
	startErr   error

	calls     []string
	inspected []string
	pulled    []string
	removed   []string
	started   []string
	created   *container.Config
}

func (f *fakeSidecarBootClient) ContainerInspect(ctx context.Context, name string) (container.InspectResponse, error) {
	f.calls = append(f.calls, "inspect")
	f.inspected = append(f.inspected, name)
	if f.inspectErr != nil {
		return container.InspectResponse{}, f.inspectErr
	}
	if f.state == nil {
		return container.InspectResponse{}, fmt.Errorf("no such container: %s: %w", name, errdefs.ErrNotFound)
	}
	return container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{ID: "existing-sidecar", State: f.state},
		Config:            &container.Config{},
	}, nil
}

func (f *fakeSidecarBootClient) ContainerRemove(ctx context.Context, name string, options container.RemoveOptions) error {
	f.calls = append(f.calls, "remove")
	f.removed = append(f.removed, name)
	return f.removeErr
}

func (f *fakeSidecarBootClient) ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, name string) (container.CreateResponse, error) {
	f.calls = append(f.calls, "create")
	f.created = config
	return container.CreateResponse{ID: "new-sidecar"}, nil
}

func (f *fakeSidecarBootClient) ContainerStart(ctx context.Context, id string, options container.StartOptions) error {
	f.calls = append(f.calls, "start")
	f.started = append(f.started, id)
	return f.startErr
}

func (f *fakeSidecarBootClient) ImagePull(ctx context.Context, ref string, options image.PullOptions) (io.ReadCloser, error) {
	f.calls = append(f.calls, "pull")
	f.pulled = append(f.pulled, ref)
	return io.NopCloser(strings.NewReader("")), nil
}

// bootSidecarDispatch drives the dispatch against the fake with the control
// seam configured, so no key is minted and no tailnet is contacted, and with
// the login wait stubbed. It reports whether the login wait ran.
func bootSidecarDispatch(t *testing.T, client *fakeSidecarBootClient) (bool, error) {
	t.Helper()
	t.Setenv(envControlURL, "https://headscale.test")
	t.Setenv(envAuthKey, "key-abc")

	ts := NewTailscale(&Namespace{name: "once"})
	loginWaited := false
	ts.awaitLogin = func(context.Context) error {
		loginWaited = true
		return nil
	}
	err := ts.bootSidecarWithClient(t.Context(), client, TailscaleSettings{Mode: TailscaleModeSidecar})
	return loginWaited, err
}

func TestBootSidecarRebuildsCrashLoopingContainer(t *testing.T) {
	// A wedged sidecar's baked-in auth key may be dead, so it is removed and
	// rebuilt with a fresh one rather than started back into its loop.
	client := &fakeSidecarBootClient{state: &container.State{Restarting: true}}

	loginWaited, err := bootSidecarDispatch(t, client)
	require.NoError(t, err)

	assert.Equal(t, []string{"inspect", "remove", "pull", "create", "start"}, client.calls)
	assert.Equal(t, []string{"once-tailscale"}, client.inspected)
	assert.Equal(t, []string{"once-tailscale"}, client.removed)
	assert.Equal(t, []string{"new-sidecar"}, client.started)
	assert.True(t, loginWaited)
	// The rebuilt container carries a re-supplied key, not the dead one.
	require.NotNil(t, client.created)
	assert.Contains(t, client.created.Env, "TS_AUTHKEY=key-abc")
}

func TestBootSidecarAbortsRebuildWhenRemoveFails(t *testing.T) {
	client := &fakeSidecarBootClient{
		state:     &container.State{Restarting: true},
		removeErr: errors.New("container is marked for removal"),
	}

	loginWaited, err := bootSidecarDispatch(t, client)

	assert.ErrorContains(t, err, "removing once-tailscale")
	assert.Equal(t, []string{"inspect", "remove"}, client.calls)
	assert.False(t, loginWaited)
}

func TestBootSidecarRebuildsContainerExitedNonzero(t *testing.T) {
	client := &fakeSidecarBootClient{state: &container.State{Status: "exited", ExitCode: 1}}

	loginWaited, err := bootSidecarDispatch(t, client)
	require.NoError(t, err)

	assert.Equal(t, []string{"inspect", "remove", "pull", "create", "start"}, client.calls)
	assert.True(t, loginWaited)
}

func TestBootSidecarStartsStoppedContainer(t *testing.T) {
	// A clean exit is not wedged: start it back up rather than rebuilding.
	client := &fakeSidecarBootClient{state: &container.State{Status: "exited"}}

	loginWaited, err := bootSidecarDispatch(t, client)
	require.NoError(t, err)

	assert.Equal(t, []string{"inspect", "start"}, client.calls)
	assert.Equal(t, []string{"existing-sidecar"}, client.started)
	assert.True(t, loginWaited)
}

func TestBootSidecarLeavesRunningContainerAlone(t *testing.T) {
	client := &fakeSidecarBootClient{state: &container.State{Status: "running", Running: true}}

	loginWaited, err := bootSidecarDispatch(t, client)
	require.NoError(t, err)

	assert.Equal(t, []string{"inspect"}, client.calls)
	assert.True(t, loginWaited)
}

func TestBootSidecarCreatesMissingContainer(t *testing.T) {
	client := &fakeSidecarBootClient{}

	loginWaited, err := bootSidecarDispatch(t, client)
	require.NoError(t, err)

	assert.Equal(t, []string{"inspect", "pull", "create", "start"}, client.calls)
	assert.Equal(t, []string{"once-tailscale"}, client.inspected)
	assert.Equal(t, []string{tailscaleSidecarImage}, client.pulled)
	assert.Empty(t, client.removed)
	assert.True(t, loginWaited)
	// The control seam supplies the key and control URL, so nothing is minted.
	require.NotNil(t, client.created)
	assert.Contains(t, client.created.Env, "TS_AUTHKEY=key-abc")
	assert.Contains(t, client.created.Env, "TS_EXTRA_ARGS=--login-server=https://headscale.test")
}

func TestBootSidecarSurfacesInspectFailure(t *testing.T) {
	client := &fakeSidecarBootClient{inspectErr: errors.New("dial unix /var/run/docker.sock: connect: permission denied")}

	loginWaited, err := bootSidecarDispatch(t, client)

	assert.ErrorContains(t, err, "inspecting tailscale sidecar")
	assert.Equal(t, []string{"inspect"}, client.calls)
	assert.False(t, loginWaited)
}

func TestBootSidecarRemovesContainerThatFailsToStart(t *testing.T) {
	client := &fakeSidecarBootClient{startErr: errors.New("no such image")}

	loginWaited, err := bootSidecarDispatch(t, client)

	assert.ErrorContains(t, err, "starting tailscale sidecar")
	assert.Equal(t, []string{"inspect", "pull", "create", "start", "remove"}, client.calls)
	assert.Equal(t, []string{"new-sidecar"}, client.removed)
	assert.False(t, loginWaited)
}

func TestSidecarLoginWaitDefaultsToRealImplementation(t *testing.T) {
	// Production leaves the hook unset, so the wait runs against the real engine
	// (docker exec into the sidecar) - proven by the nil-client panic, the
	// package's convention for asserting an engine call without a daemon.
	ts := NewTailscale(&Namespace{name: "once"})
	require.Nil(t, ts.awaitLogin)
	assert.Panics(t, func() { _ = ts.awaitSidecarLogin(t.Context()) })
}
