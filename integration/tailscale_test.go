package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
)

// Pinned images for the Tailscale integration harness. Bumped via Once releases,
// mirroring how the kamal-proxy pin is managed.
const (
	headscaleImage = "headscale/headscale:0.26.1"
	tsdproxyImage  = "almeidapaulopt/tsdproxy:2"
	whoamiImage    = "traefik/whoami:v1.10.3"

	headscaleBin = "/ko-app/headscale" // ko image has no shell; exec the binary directly
	testUser     = "once-test"
)

// controlPlane abstracts the Tailscale control server the harness registers
// nodes against. headscale (default) runs locally inside the test's Docker
// network with no Tailscale-SaaS dependency; tailscale-saas is a seam for later
// slices and is not implemented here.
type controlPlane interface {
	// controlURL is the value tsdproxy uses as its controlUrl.
	controlURL() string
	// authKey mints (or returns) a reusable auth key for registering nodes.
	authKey(ctx context.Context) (string, error)
	// nodeNames lists the hostnames currently registered with the control plane.
	nodeNames(ctx context.Context) ([]string, error)
}

type controlBackend string

const (
	backendHeadscale controlBackend = "headscale"
	backendSaaS      controlBackend = "tailscale-saas"
)

// newControlPlane selects the control-plane backend. headscale is the default;
// tailscale-saas is recognised but unimplemented in this slice.
func newControlPlane(t *testing.T, ctx context.Context, ns *docker.Namespace) controlPlane {
	t.Helper()

	backend := controlBackend(os.Getenv("ONCE_TEST_TS_BACKEND"))
	if backend == "" {
		backend = backendHeadscale
	}

	switch backend {
	case backendHeadscale:
		return startHeadscale(t, ctx, ns)
	case backendSaaS:
		// ponytail: seam only. Later slices implement the SaaS path behind this
		// same interface (OAuth, real ts.net FQDNs); headscale proves the rest.
		t.Skip("tailscale-saas control-plane backend is a seam; not implemented in this slice")
		return nil
	default:
		t.Fatalf("unknown control-plane backend %q", backend)
		return nil
	}
}

// headscale is the local open-source control server backend.
type headscale struct {
	t      *testing.T
	cli    *client.Client
	name   string // container name, also its DNS name within the network
	userID int
}

func (h *headscale) controlURL() string {
	return fmt.Sprintf("http://%s:8080", h.name)
}

func (h *headscale) authKey(ctx context.Context) (string, error) {
	out, err := execCapture(ctx, h.cli, h.name, headscaleBin,
		"preauthkeys", "create", "--user", strconv.Itoa(h.userID), "--reusable", "--expiration", "24h", "-o", "json")
	if err != nil {
		return "", err
	}
	var key struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(out), &key); err != nil {
		return "", fmt.Errorf("parsing preauthkey: %w (%s)", err, out)
	}
	return key.Key, nil
}

func (h *headscale) nodeNames(ctx context.Context) ([]string, error) {
	out, err := execCapture(ctx, h.cli, h.name, headscaleBin, "nodes", "list", "-o", "json")
	if err != nil {
		return nil, err
	}
	// given_name is headscale's canonical, de-duplicated MagicDNS hostname.
	var nodes []struct {
		GivenName string `json:"given_name"`
	}
	if err := json.Unmarshal([]byte(out), &nodes); err != nil {
		return nil, fmt.Errorf("parsing nodes: %w (%s)", err, out)
	}
	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.GivenName
	}
	return names, nil
}

// startHeadscale boots a headscale container in the namespace network, waits for
// it to be ready, and creates the once-test user.
func startHeadscale(t *testing.T, ctx context.Context, ns *docker.Namespace) *headscale {
	t.Helper()

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	t.Cleanup(func() { cli.Close() })

	pullImage(t, ctx, cli, headscaleImage)

	name := ns.Name() + "-headscale"
	configPath := writeTempFile(t, "headscale.yaml", fmt.Sprintf(headscaleConfig, name))

	runContainer(t, ctx, cli, ns, name,
		&container.Config{Image: headscaleImage, Cmd: []string{"serve"}},
		&container.HostConfig{Binds: []string{configPath + ":/etc/headscale/config.yaml:ro"}},
	)

	// Ready once the control socket answers the CLI.
	require.Eventually(t, func() bool {
		_, err := execCapture(ctx, cli, name, headscaleBin, "users", "list", "-o", "json")
		return err == nil
	}, 30*time.Second, time.Second, "headscale did not become ready")

	_, err = execCapture(ctx, cli, name, headscaleBin, "users", "create", testUser)
	require.NoError(t, err)

	out, err := execCapture(ctx, cli, name, headscaleBin, "users", "list", "-o", "json")
	require.NoError(t, err)
	var users []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &users))
	userID := -1
	for _, u := range users {
		if u.Name == testUser {
			userID = u.ID
		}
	}
	require.NotEqual(t, -1, userID, "once-test user not found")

	return &headscale{t: t, cli: cli, name: name, userID: userID}
}

// startWhoami boots a raw traefik/whoami container labelled for tsdproxy to pick
// up as a plain-HTTP (no TLS, no Funnel) tailnet proxy.
func startWhoami(t *testing.T, ctx context.Context, ns *docker.Namespace, name string) {
	t.Helper()

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	defer cli.Close()

	pullImage(t, ctx, cli, whoamiImage)

	runContainer(t, ctx, cli, ns, ns.Name()+"-whoami",
		&container.Config{
			Image: whoamiImage,
			Labels: map[string]string{
				"tsdproxy.enable": "true",
				"tsdproxy.name":   name,
				// Plain HTTP both sides: headscale has no ts.net certs. No tailscale_funnel option.
				"tsdproxy.port.1": "80/http:80/http",
			},
		},
		&container.HostConfig{},
	)
}

// startTSDProxy boots a raw tsdproxy container pointed at the control plane via
// controlUrl + authKey (not via Once's boot code, which does not exist yet).
func startTSDProxy(t *testing.T, ctx context.Context, ns *docker.Namespace, cp controlPlane) {
	t.Helper()

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	defer cli.Close()

	pullImage(t, ctx, cli, tsdproxyImage)

	key, err := cp.authKey(ctx)
	require.NoError(t, err)

	configPath := writeTempFile(t, "tsdproxy.yaml", fmt.Sprintf(tsdproxyConfig, key, cp.controlURL()))

	runContainer(t, ctx, cli, ns, ns.Name()+"-tsdproxy",
		&container.Config{
			Image:   tsdproxyImage,
			Volumes: map[string]struct{}{"/data": {}}, // dataDir must exist; anonymous volume creates it
		},
		&container.HostConfig{
			Binds: []string{
				configPath + ":/config/tsdproxy.yaml:ro",
				"/var/run/docker.sock:/var/run/docker.sock",
			},
		},
	)
}

func TestTSDProxyRegistersWithHeadscale(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ns, err := docker.NewNamespace("once-tailscale-test")
	require.NoError(t, err)
	require.NoError(t, ns.EnsureNetwork(ctx))
	t.Cleanup(func() { ns.Teardown(context.Background(), true) })

	cp := newControlPlane(t, ctx, ns)

	startWhoami(t, ctx, ns, "whoami")
	startTSDProxy(t, ctx, ns, cp)

	require.Eventually(t, func() bool {
		names, err := cp.nodeNames(ctx)
		return err == nil && slices.Contains(names, "whoami")
	}, 90*time.Second, 2*time.Second, "tsdproxy node never registered with the control plane")
}

// TestTailscaleEnableBootsTSDProxyViaControlSeam boots once-tsdproxy through
// Once's real Enable path (not the raw harness helper), driving it at headscale
// via the hidden control seam, and asserts a labelled app registers a node.
// Disable then tears down the container while keeping the data volume.
func TestTailscaleEnableBootsTSDProxyViaControlSeam(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ns, err := docker.NewNamespace("once-ts-enable-test")
	require.NoError(t, err)
	require.NoError(t, ns.EnsureNetwork(ctx))
	t.Cleanup(func() { ns.Teardown(context.Background(), true) })

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	t.Cleanup(func() { cli.Close() })

	cp := newControlPlane(t, ctx, ns)

	key, err := cp.authKey(ctx)
	require.NoError(t, err)
	t.Setenv("ONCE_TAILSCALE_CONTROL_URL", cp.controlURL())
	t.Setenv("ONCE_TAILSCALE_AUTH_KEY", key)

	startWhoami(t, ctx, ns, "whoami")

	require.NoError(t, ns.Tailscale().Enable(ctx, docker.TailscaleSettings{
		ClientID:     "unused-with-control-seam",
		ClientSecret: "unused-with-control-seam",
	}))

	tsdproxyName := ns.Name() + "-tsdproxy"
	info, err := cli.ContainerInspect(ctx, tsdproxyName)
	require.NoError(t, err)
	assert.Contains(t, info.Config.Labels["once"], "unused-with-control-seam")

	require.Eventually(t, func() bool {
		names, err := cp.nodeNames(ctx)
		return err == nil && slices.Contains(names, "whoami")
	}, 90*time.Second, 2*time.Second, "Once-booted tsdproxy never registered a node via the control seam")

	dataVolume := ns.Name() + "-tsdproxy-data"
	_, err = cli.VolumeInspect(ctx, dataVolume)
	require.NoError(t, err, "data volume should exist after enable")

	require.NoError(t, ns.Tailscale().Disable(ctx))

	_, err = cli.ContainerInspect(ctx, tsdproxyName)
	assert.True(t, errdefs.IsNotFound(err), "container should be gone after disable")

	_, err = cli.VolumeInspect(ctx, dataVolume)
	assert.NoError(t, err, "data volume should be retained after disable")
}

func TestWhoamiDeploysViaHarness(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ns, err := docker.NewNamespace("once-whoami-test")
	require.NoError(t, err)
	defer ns.Teardown(ctx, true)

	require.NoError(t, ns.EnsureNetwork(ctx))
	require.NoError(t, ns.Proxy().Boot(ctx, getProxyPorts(t)))

	app := deployApp(t, ctx, ns, docker.ApplicationSettings{
		Name:  "whoami",
		Image: whoamiImage,
		Host:  "whoami.localhost",
	})

	require.NotNil(t, app)
	assert.Equal(t, "whoami", app.Settings.Name)
	assert.True(t, ns.HostInUse("whoami.localhost"))
}

// Helpers

func runContainer(t *testing.T, ctx context.Context, cli *client.Client, ns *docker.Namespace, name string, cfg *container.Config, host *container.HostConfig) {
	t.Helper()

	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{ns.Name(): {}},
	}

	resp, err := cli.ContainerCreate(ctx, cfg, host, netCfg, nil, name)
	require.NoError(t, err)
	t.Cleanup(func() {
		cli.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{Force: true, RemoveVolumes: true})
	})

	require.NoError(t, cli.ContainerStart(ctx, resp.ID, container.StartOptions{}))
}

func execCapture(ctx context.Context, cli *client.Client, containerName string, cmd ...string) (string, error) {
	execResp, err := cli.ContainerExecCreate(ctx, containerName, container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return "", err
	}

	resp, err := cli.ContainerExecAttach(ctx, execResp.ID, container.ExecStartOptions{})
	if err != nil {
		return "", err
	}
	defer resp.Close()

	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, resp.Reader); err != nil {
		return "", err
	}

	inspect, err := cli.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return "", err
	}
	if inspect.ExitCode != 0 {
		return stdout.String(), fmt.Errorf("exec %v exited %d: %s", cmd, inspect.ExitCode, stderr.String())
	}
	return stdout.String(), nil
}

func pullImage(t *testing.T, ctx context.Context, cli *client.Client, ref string) {
	t.Helper()
	reader, err := cli.ImagePull(ctx, ref, image.PullOptions{})
	require.NoError(t, err)
	defer reader.Close()
	_, err = bytes.NewBuffer(nil).ReadFrom(reader)
	require.NoError(t, err)
}

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

// headscaleConfig is a minimal headscale config. %[1]s is the container/DNS name.
// Embedded DERP avoids any internet dependency; plain HTTP, no ts.net certs.
const headscaleConfig = `server_url: http://%[1]s:8080
listen_addr: 0.0.0.0:8080
metrics_listen_addr: 0.0.0.0:9090
grpc_listen_addr: 127.0.0.1:50443
noise:
  private_key_path: /var/lib/headscale/noise_private.key
prefixes:
  v4: 100.64.0.0/10
  v6: fd7a:115c:a1e0::/48
database:
  type: sqlite
  sqlite:
    path: /var/lib/headscale/db.sqlite
derp:
  server:
    enabled: true
    region_id: 999
    region_code: headscale
    region_name: Headscale Embedded DERP
    stun_listen_addr: 0.0.0.0:3478
    private_key_path: /var/lib/headscale/derp_server_private.key
  urls: []
  auto_update_enabled: false
  update_frequency: 24h
disable_check_updates: true
dns:
  magic_dns: true
  base_domain: tailnet.test
  override_local_dns: false
  nameservers:
    global: []
unix_socket: /var/run/headscale/headscale.sock
unix_socket_permission: "0770"
log:
  level: info
  format: text
`

// tsdproxyConfig points the default provider at the control plane. %[1]s is the
// auth key, %[2]s is the controlUrl. No dashboard key (rejected by strict parse).
const tsdproxyConfig = `defaultProxyProvider: default
docker:
  local:
    host: unix:///var/run/docker.sock
    targetHostname: host.docker.internal
    defaultProxyProvider: default
tailscale:
  providers:
    default:
      authKey: "%[1]s"
      authKeyFile: ""
      controlUrl: %[2]s
  dataDir: /data/
http:
  hostname: 0.0.0.0
  port: 8080
log:
  level: info
  json: false
`
