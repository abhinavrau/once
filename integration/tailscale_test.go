package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
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

	"github.com/basecamp/once/internal/background"
	"github.com/basecamp/once/internal/docker"
)

// Pinned images for the Tailscale integration harness. Bumped via Once releases,
// mirroring how the kamal-proxy pin is managed.
const (
	headscaleImage  = "headscale/headscale:0.26.1"
	tsdproxyImage   = "almeidapaulopt/tsdproxy:2"
	whoamiImage     = "traefik/whoami:v1.10.3"
	tailscaleImage  = "tailscale/tailscale:v1.80.3"
	magicDNSBaseDom = "tailnet.test" // matches base_domain in headscaleConfig

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
	// nodeOnline reports whether the named node is currently connected. found is
	// false when no node with that name exists.
	nodeOnline(ctx context.Context, name string) (found, online bool, err error)
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

func (h *headscale) nodeOnline(ctx context.Context, name string) (bool, bool, error) {
	out, err := execCapture(ctx, h.cli, h.name, headscaleBin, "nodes", "list", "-o", "json")
	if err != nil {
		return false, false, err
	}
	var nodes []struct {
		GivenName string `json:"given_name"`
		Online    bool   `json:"online"`
	}
	if err := json.Unmarshal([]byte(out), &nodes); err != nil {
		return false, false, fmt.Errorf("parsing nodes: %w (%s)", err, out)
	}
	for _, n := range nodes {
		if n.GivenName == name {
			return true, n.Online, nil
		}
	}
	return false, false, nil
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

// TestTeardownRemovesTailscaleSystemResources proves the full-cleanup path:
// after enabling Tailscale and booting once-admin, teardown removes both system
// containers AND deletes the data volume (unlike disable, which retains it).
// No control plane is needed — cleanup doesn't depend on tailnet registration.
func TestTeardownRemovesTailscaleSystemResources(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ns, err := docker.NewNamespace("once-ts-teardown-test")
	require.NoError(t, err)
	require.NoError(t, ns.EnsureNetwork(ctx))
	t.Cleanup(func() { ns.Teardown(context.Background(), true) })

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	t.Cleanup(func() { cli.Close() })

	// Run the daemon's admin socket server so once-admin's bind mount sees a real
	// socket; point the control seam at a dummy so Enable creates the container
	// without attempting OAuth.
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", t.TempDir())
	t.Setenv("ONCE_TAILSCALE_CONTROL_URL", "http://invalid.control.test:8080")
	t.Setenv("ONCE_TAILSCALE_AUTH_KEY", "dummy-auth-key")
	adminCtx, stopAdmin := context.WithCancel(ctx)
	t.Cleanup(stopAdmin)
	go func() { _ = background.NewAdminServer().Run(adminCtx) }()
	require.Eventually(t, func() bool {
		_, err := net.Dial("unix", docker.AdminSocketPath())
		return err == nil
	}, 5*time.Second, 50*time.Millisecond, "admin socket never became available")

	require.NoError(t, ns.Tailscale().Enable(ctx, docker.TailscaleSettings{
		ClientID:     "unused-with-control-seam",
		ClientSecret: "unused-with-control-seam",
	}))
	require.NoError(t, ns.Admin().Boot(ctx))

	tsdproxyName := ns.Name() + "-tsdproxy"
	adminName := ns.Name() + "-admin"
	dataVolume := ns.Name() + "-tsdproxy-data"

	_, err = cli.ContainerInspect(ctx, tsdproxyName)
	require.NoError(t, err, "tsdproxy container should exist after enable")
	_, err = cli.ContainerInspect(ctx, adminName)
	require.NoError(t, err, "admin container should exist after boot")
	_, err = cli.VolumeInspect(ctx, dataVolume)
	require.NoError(t, err, "data volume should exist after enable")

	require.NoError(t, ns.Teardown(ctx, true))

	_, err = cli.ContainerInspect(ctx, tsdproxyName)
	assert.True(t, errdefs.IsNotFound(err), "tsdproxy container should be gone after teardown")
	_, err = cli.ContainerInspect(ctx, adminName)
	assert.True(t, errdefs.IsNotFound(err), "admin container should be gone after teardown")
	_, err = cli.VolumeInspect(ctx, dataVolume)
	assert.True(t, errdefs.IsNotFound(err), "data volume should be deleted by teardown")
}

// TestTeardownNoopWhenTailscaleNeverEnabled proves teardown is safe and
// idempotent for the Tailscale resources when Tailscale was never enabled: no
// tsdproxy/admin containers or data volume exist, and teardown still succeeds.
func TestTeardownNoopWhenTailscaleNeverEnabled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	ns, err := docker.NewNamespace("once-ts-teardown-noop-test")
	require.NoError(t, err)
	require.NoError(t, ns.EnsureNetwork(ctx))
	t.Cleanup(func() { ns.Teardown(context.Background(), true) })

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	t.Cleanup(func() { cli.Close() })

	require.NoError(t, ns.Teardown(ctx, true), "teardown should no-op for absent Tailscale resources")

	_, err = cli.ContainerInspect(ctx, ns.Name()+"-tsdproxy")
	assert.True(t, errdefs.IsNotFound(err), "tsdproxy container should be absent")
	_, err = cli.ContainerInspect(ctx, ns.Name()+"-admin")
	assert.True(t, errdefs.IsNotFound(err), "admin container should be absent")
	_, err = cli.VolumeInspect(ctx, ns.Name()+"-tsdproxy-data")
	assert.True(t, errdefs.IsNotFound(err), "data volume should be absent")
}

// TestAppRetrofitRollOnEnableAndDisable deploys a real Once-managed whoami app,
// then enables Tailscale and asserts the retrofit roll injects the tsdproxy.*
// labels (alongside the existing once label) so the app's ephemeral node
// registers with headscale. Disabling rolls the app again to strip the labels.
// Removing the app takes its ephemeral node offline and frees the Magic DNS name.
func TestAppRetrofitRollOnEnableAndDisable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ns, err := docker.NewNamespace("once-ts-retrofit-test")
	require.NoError(t, err)
	require.NoError(t, ns.EnsureNetwork(ctx))
	require.NoError(t, ns.Proxy().Boot(ctx, getProxyPorts(t)))
	t.Cleanup(func() { ns.Teardown(context.Background(), true) })

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	t.Cleanup(func() { cli.Close() })

	cp := newControlPlane(t, ctx, ns)
	key, err := cp.authKey(ctx)
	require.NoError(t, err)
	t.Setenv("ONCE_TAILSCALE_CONTROL_URL", cp.controlURL())
	t.Setenv("ONCE_TAILSCALE_AUTH_KEY", key)

	deployApp(t, ctx, ns, docker.ApplicationSettings{
		Name:  "whoami",
		Image: whoamiImage,
		Host:  "whoami.localhost",
	})

	// Enable retrofits the running app: tsdproxy.* labels are injected via a roll.
	require.NoError(t, ns.Tailscale().Enable(ctx, docker.TailscaleSettings{
		ClientID:     "unused-with-control-seam",
		ClientSecret: "unused-with-control-seam",
	}))

	assertAppLabels(t, ctx, cli, ns, hasTSDProxyLabels)

	require.Eventually(t, func() bool {
		names, err := cp.nodeNames(ctx)
		return err == nil && slices.Contains(names, "whoami")
	}, 90*time.Second, 2*time.Second, "retrofitted app never registered a node on enable")

	// Disable rolls the app again, stripping the tsdproxy.* labels.
	require.NoError(t, ns.Tailscale().Disable(ctx))
	assertAppLabels(t, ctx, cli, ns, noTSDProxyLabels)

	// Re-enable so we can prove ephemeral cleanup on delete frees the name.
	require.NoError(t, ns.Tailscale().Enable(ctx, docker.TailscaleSettings{
		ClientID:     "unused-with-control-seam",
		ClientSecret: "unused-with-control-seam",
	}))
	require.Eventually(t, func() bool {
		names, err := cp.nodeNames(ctx)
		return err == nil && slices.Contains(names, "whoami")
	}, 90*time.Second, 2*time.Second, "app never re-registered after re-enable")

	require.NoError(t, ns.Refresh(ctx))
	require.NoError(t, ns.Application("whoami").Remove(ctx, true))

	// Deleting the app takes its node offline. Against headscale that is the
	// observable signal; SaaS-side auto-removal that frees the Magic DNS name is
	// covered by the SaaS smoke pass (headscale may not replicate it exactly).
	require.Eventually(t, func() bool {
		found, online, err := cp.nodeOnline(ctx, "whoami")
		return err == nil && (!found || !online)
	}, 90*time.Second, 2*time.Second, "node never went offline after app deletion")
}

// TestAppReachableViaMagicDNS is the single end-to-end data-path smoke: it boots
// a tailscale client joined to the same headscale, then curls the retrofitted
// whoami app through the tailnet via its Magic DNS name and asserts the echoed
// hostname is the app container — proving userspace networking carries traffic.
func TestAppReachableViaMagicDNS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ns, err := docker.NewNamespace("once-ts-magicdns-test")
	require.NoError(t, err)
	require.NoError(t, ns.EnsureNetwork(ctx))
	require.NoError(t, ns.Proxy().Boot(ctx, getProxyPorts(t)))
	t.Cleanup(func() { ns.Teardown(context.Background(), true) })

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	t.Cleanup(func() { cli.Close() })

	cp := newControlPlane(t, ctx, ns)
	key, err := cp.authKey(ctx)
	require.NoError(t, err)
	t.Setenv("ONCE_TAILSCALE_CONTROL_URL", cp.controlURL())
	t.Setenv("ONCE_TAILSCALE_AUTH_KEY", key)

	deployApp(t, ctx, ns, docker.ApplicationSettings{
		Name:  "whoami",
		Image: whoamiImage,
		Host:  "whoami.localhost",
	})
	require.NoError(t, ns.Tailscale().Enable(ctx, docker.TailscaleSettings{
		ClientID:     "unused-with-control-seam",
		ClientSecret: "unused-with-control-seam",
	}))

	require.Eventually(t, func() bool {
		found, online, err := cp.nodeOnline(ctx, "whoami")
		return err == nil && found && online
	}, 90*time.Second, 2*time.Second, "app node never came online before the smoke")

	// The app container's hostname is what whoami echoes back through the proxy.
	require.NoError(t, ns.Refresh(ctx))
	appContainer, err := ns.Application("whoami").ContainerName(ctx)
	require.NoError(t, err)
	appInfo, err := cli.ContainerInspect(ctx, appContainer)
	require.NoError(t, err)
	appHostname := appInfo.Config.Hostname

	clientName := startTailscaleClient(t, ctx, cli, ns, cp, key)

	magicDNSURL := fmt.Sprintf("http://whoami.%s/", magicDNSBaseDom)
	var body string
	var lastErr error
	ok := assert.Eventually(t, func() bool {
		out, err := execCapture(ctx, cli, clientName, "sh", "-c",
			"http_proxy=http://localhost:1055 wget -T 5 -qO- "+magicDNSURL)
		if err != nil {
			lastErr = err
			return false
		}
		body = out
		return true
	}, 120*time.Second, 3*time.Second)
	if !ok {
		status, _ := execCapture(ctx, cli, clientName, "tailscale", "status")
		netcheck, _ := execCapture(ctx, cli, clientName, "tailscale", "netcheck")
		t.Logf("last wget error: %v\ntailscale status:\n%s\nnetcheck:\n%s", lastErr, status, netcheck)
		t.Fatal("never reached the app via Magic DNS")
	}

	assert.Contains(t, body, "Hostname: "+appHostname,
		"whoami should echo the app container hostname proving the tailnet data path")
}

// TestLookupAPIReportsRunningFQDN enables Tailscale via the control seam, then
// queries the loopback-published lookup API and asserts it reports the app proxy
// as Running with a non-empty tailnet FQDN — the host-side discovery path that
// once tailscale status and once list rely on.
func TestLookupAPIReportsRunningFQDN(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ns, err := docker.NewNamespace("once-ts-lookup-test")
	require.NoError(t, err)
	require.NoError(t, ns.EnsureNetwork(ctx))
	require.NoError(t, ns.Proxy().Boot(ctx, getProxyPorts(t)))
	t.Cleanup(func() { ns.Teardown(context.Background(), true) })

	cp := newControlPlane(t, ctx, ns)
	key, err := cp.authKey(ctx)
	require.NoError(t, err)
	t.Setenv("ONCE_TAILSCALE_CONTROL_URL", cp.controlURL())
	t.Setenv("ONCE_TAILSCALE_AUTH_KEY", key)

	deployApp(t, ctx, ns, docker.ApplicationSettings{
		Name:  "whoami",
		Image: whoamiImage,
		Host:  "whoami.localhost",
	})
	require.NoError(t, ns.Tailscale().Enable(ctx, docker.TailscaleSettings{
		ClientID:     "unused-with-control-seam",
		ClientSecret: "unused-with-control-seam",
	}))

	var whoami docker.TailnetProxy
	require.Eventually(t, func() bool {
		proxies, err := ns.Tailscale().Proxies(ctx)
		if err != nil {
			return false
		}
		for _, p := range proxies {
			if p.Name == "whoami" && p.Status == "Running" {
				whoami = p
				return true
			}
		}
		return false
	}, 90*time.Second, 2*time.Second, "lookup API never reported whoami as Running")

	assert.NotEmpty(t, whoami.URL, "running proxy should report a tailnet FQDN")

	// The domain suffix is derived from the reported FQDN and persisted into the
	// tsdproxy settings label, so app tailnet URLs can be built without a lookup.
	suffix, err := ns.Tailscale().DomainSuffix(ctx)
	require.NoError(t, err)
	assert.Equal(t, magicDNSBaseDom, suffix)

	persisted, err := ns.Tailscale().LoadSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, magicDNSBaseDom, persisted.DomainSuffix, "suffix should be persisted on the container label")
}

// TestAdminReachableViaMagicDNS proves the zero-host-port admin path end to end:
// the daemon serves a health endpoint on a Unix socket, the once-admin nginx
// container (no published ports) proxies port 80 to that socket, and TSDProxy
// exposes it on the tailnet. A tailscale client curls once-admin's Magic DNS
// name and gets the health response back.
func TestAdminReachableViaMagicDNS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ns, err := docker.NewNamespace("once-ts-admin-test")
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

	// Run the daemon's admin socket server in-process against a bind-mountable
	// temp dir, then wait for the socket so once-admin's mount sees a socket (not
	// a directory Docker would otherwise create).
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", t.TempDir())
	adminCtx, stopAdmin := context.WithCancel(ctx)
	t.Cleanup(stopAdmin)
	go func() { _ = background.NewAdminServer().Run(adminCtx) }()
	require.Eventually(t, func() bool {
		_, err := net.Dial("unix", docker.AdminSocketPath())
		return err == nil
	}, 5*time.Second, 50*time.Millisecond, "admin socket never became available")

	require.NoError(t, ns.Tailscale().Enable(ctx, docker.TailscaleSettings{
		ClientID:     "unused-with-control-seam",
		ClientSecret: "unused-with-control-seam",
	}))
	require.NoError(t, ns.Admin().Boot(ctx))

	require.Eventually(t, func() bool {
		found, online, err := cp.nodeOnline(ctx, "once-admin")
		return err == nil && found && online
	}, 90*time.Second, 2*time.Second, "once-admin node never came online")

	clientName := startTailscaleClient(t, ctx, cli, ns, cp, key)

	url := fmt.Sprintf("http://once-admin.%s/up", magicDNSBaseDom)
	var body string
	var lastErr error
	ok := assert.Eventually(t, func() bool {
		out, err := execCapture(ctx, cli, clientName, "sh", "-c",
			"http_proxy=http://localhost:1055 wget -T 5 -qO- "+url)
		if err != nil {
			lastErr = err
			return false
		}
		body = out
		return true
	}, 120*time.Second, 3*time.Second)
	if !ok {
		status, _ := execCapture(ctx, cli, clientName, "tailscale", "status")
		t.Logf("last wget error: %v\ntailscale status:\n%s", lastErr, status)
		t.Fatal("never reached once-admin via Magic DNS")
	}

	assert.Contains(t, body, "ok", "admin health endpoint should be reachable through the tailnet")
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

// startTailscaleClient boots a tailscale node (userspace, no /dev/net/tun)
// joined to the control plane and exposing an outbound HTTP proxy on :1055 that
// resolves Magic DNS, so the test can curl tailnet hostnames from inside it.
func startTailscaleClient(t *testing.T, ctx context.Context, cli *client.Client, ns *docker.Namespace, cp controlPlane, authKey string) string {
	t.Helper()

	pullImage(t, ctx, cli, tailscaleImage)

	name := ns.Name() + "-tsclient"
	runContainer(t, ctx, cli, ns, name,
		&container.Config{
			Image: tailscaleImage,
			Env: []string{
				"TS_AUTHKEY=" + authKey,
				"TS_EXTRA_ARGS=--login-server=" + cp.controlURL() + " --accept-dns=true",
				"TS_USERSPACE=true",
				"TS_OUTBOUND_HTTP_PROXY_LISTEN=0.0.0.0:1055",
				"TS_HOSTNAME=tsclient",
			},
		},
		&container.HostConfig{},
	)

	require.Eventually(t, func() bool {
		_, err := execCapture(ctx, cli, name, "tailscale", "status")
		return err == nil
	}, 90*time.Second, 2*time.Second, "tailscale client never joined the tailnet")

	return name
}

// assertAppLabels inspects the whoami app's current container and checks its
// labels with the given predicate. The once label must always survive.
func assertAppLabels(t *testing.T, ctx context.Context, cli *client.Client, ns *docker.Namespace, check func(*testing.T, map[string]string)) {
	t.Helper()
	require.NoError(t, ns.Refresh(ctx))
	name, err := ns.Application("whoami").ContainerName(ctx)
	require.NoError(t, err)
	info, err := cli.ContainerInspect(ctx, name)
	require.NoError(t, err)
	assert.NotEmpty(t, info.Config.Labels["once"], "once label must survive the roll")
	check(t, info.Config.Labels)
}

func hasTSDProxyLabels(t *testing.T, labels map[string]string) {
	t.Helper()
	assert.Equal(t, "true", labels["tsdproxy.enable"])
	assert.Equal(t, "whoami", labels["tsdproxy.name"])
	assert.Equal(t, "80/http:80/http", labels["tsdproxy.port.1"])
	assert.Equal(t, "true", labels["tsdproxy.ephemeral"])
}

func noTSDProxyLabels(t *testing.T, labels map[string]string) {
	t.Helper()
	assert.NotContains(t, labels, "tsdproxy.enable")
}

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
