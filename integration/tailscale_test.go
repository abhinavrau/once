package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
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

// Pinned images for the Tailscale integration harness. Bumped via Once
// releases, mirroring how the kamal-proxy pin is managed.
const (
	headscaleImage  = "headscale/headscale:0.26.1"
	whoamiImage     = "traefik/whoami:v1.10.3"
	magicDNSBaseDom = "tailnet.test" // matches base_domain in headscaleConfig

	headscaleBin = "/ko-app/headscale" // ko image has no shell; exec the binary directly
	testUser     = "once-test"
)

// controlPlane abstracts the Tailscale control server the harness registers
// nodes against. headscale (default) runs locally inside the test's Docker
// network with no Tailscale-SaaS dependency; tailscale-saas is a seam for later
// slices and is not implemented here.
type controlPlane interface {
	// controlURL is the value the once-tailscale sidecar joins via the control seam.
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

// useControlSeam points Once's hidden control seam at the control plane, so
// the once-tailscale sidecar joins it with a pre-minted key instead of OAuth
// and docktail runs in manual mode (no Tailscale cloud API).
func useControlSeam(t *testing.T, ctx context.Context, cp controlPlane) {
	t.Helper()
	key, err := cp.authKey(ctx)
	require.NoError(t, err)
	t.Setenv("ONCE_TAILSCALE_CONTROL_URL", cp.controlURL())
	t.Setenv("ONCE_TAILSCALE_AUTH_KEY", key)
}

// hideHostTailscaled points host-tailscaled detection at an empty temp dir, so
// a fresh enable never generates the --mode host notice on machines running
// their own tailscaled (the sidecar is the default either way).
func hideHostTailscaled(t *testing.T) {
	t.Helper()
	t.Setenv("ONCE_TAILSCALED_DIR", t.TempDir())
}

// plantHostTailscaledSocket fakes a host tailscaled by its socket file in a
// temp dir and points Once at it. Returns the dir for bind-mount assertions.
func plantHostTailscaledSocket(t *testing.T) string {
	t.Helper()
	hostDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(hostDir, "tailscaled.sock"), nil, 0o644))
	t.Setenv("ONCE_TAILSCALED_DIR", hostDir)
	return hostDir
}

// startAdminServer runs the daemon's admin socket server in-process and waits
// for its socket, so the once-admin container the enable lifecycle boots has a
// real socket to bind-mount.
func startAdminServer(t *testing.T, ctx context.Context) {
	t.Helper()
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", t.TempDir())
	adminCtx, stopAdmin := context.WithCancel(ctx)
	t.Cleanup(stopAdmin)
	go func() { _ = background.NewAdminServer().Run(adminCtx) }()
	require.Eventually(t, func() bool {
		_, err := net.Dial("unix", docker.AdminSocketPath())
		return err == nil
	}, 5*time.Second, 50*time.Millisecond, "admin socket never became available")
}

// plantModelessDocktail replaces the once-docktail container with a stopped
// stand-in whose once label carries the given settings without a mode, faking a
// corrupt or legacy install for the self-heal path. whoamiImage stands in for
// the docktail image: nothing runs it, the next enable rebuilds the topology.
func plantModelessDocktail(t *testing.T, ctx context.Context, cli *client.Client, name string, settings docker.TailscaleSettings) {
	t.Helper()
	settings.Mode = ""
	require.NoError(t, cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: true}))
	_, err := cli.ContainerCreate(ctx,
		&container.Config{Image: whoamiImage, Labels: map[string]string{"once": settings.Marshal()}},
		nil, nil, nil, name)
	require.NoError(t, err)
}

// plantTopologyLessDocktail replaces the once-docktail container with a
// stopped stand-in carrying a label with no "topology" key at all - a
// pre-ADR-0015 install - for the older-Once self-heal path.
// docker.TailscaleSettings.Marshal always writes "topology" (not omitempty),
// so the label is built by hand rather than through it.
func plantTopologyLessDocktail(t *testing.T, ctx context.Context, cli *client.Client, name string, settings docker.TailscaleSettings) {
	t.Helper()
	require.NoError(t, cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: true}))
	label := fmt.Sprintf(`{"clientId":%q,"clientSecret":%q,"tag":%q,"domainSuffix":%q,"mode":%q}`,
		settings.ClientID, settings.ClientSecret, settings.Tag, settings.DomainSuffix, settings.Mode)
	_, err := cli.ContainerCreate(ctx,
		&container.Config{Image: whoamiImage, Labels: map[string]string{"once": label}},
		nil, nil, nil, name)
	require.NoError(t, err)
}

// controlSeamSettings are the TailscaleSettings used with the control seam,
// where OAuth credentials are never exercised - EnableTailscale drops them
// before they reach the label, so they are set here to prove exactly that.
// Topology is explicit (ticket 03: a fresh enable otherwise defaults to
// nodes, which has no adapter until ticket 04) so this suite keeps exercising
// the Docktail/sidecar topology it was written for.
func controlSeamSettings() docker.TailscaleSettings {
	return docker.TailscaleSettings{
		ClientID:     "unused-with-control-seam",
		ClientSecret: "unused-with-control-seam",
		Topology:     docker.TailnetTopologyServices,
	}
}

// TestTailscaleEnableBootsDocktailTopology drives the tailnet exposure end to
// end against headscale: enable boots the unprivileged once-tailscale sidecar
// and once-docktail, the sidecar joins the control plane, and the Magic DNS
// suffix is read from the local daemon and persisted. A `--expose tailnet` app
// is advertised as a Service named after its Host and registers no proxy
// route, while local apps route through the proxy and advertise nothing -
// exactly one routing path per exposure, with `update --expose` moving apps
// between them. Disable tears everything down, keeping the sidecar's state
// volume, and strips the labels again.
func TestTailscaleEnableBootsDocktailTopology(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	nsName := "once-ts-enable-test"
	ns, err := docker.NewNamespace(nsName)
	require.NoError(t, err)
	require.NoError(t, ns.EnsureNetwork(ctx))
	bootProxy(t, ctx, ns, getProxyPorts(t))
	t.Cleanup(func() { ns.Teardown(context.Background(), true) })

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	t.Cleanup(func() { cli.Close() })

	cp := newControlPlane(t, ctx, ns)
	useControlSeam(t, ctx, cp)

	// A host tailscaled must no longer decide the mode (ADR-0007): plant its
	// socket and a fresh enable still defaults to the sidecar, leading its
	// warnings with the one-line notice that points at --mode host.
	plantHostTailscaledSocket(t)
	startAdminServer(t, ctx)

	proxyRoutes := func() string {
		out, err := ns.Proxy().ExecOutput(ctx, []string{"kamal-proxy", "list"})
		require.NoError(t, err)
		return out
	}

	deployApp(t, ctx, ns, docker.ApplicationSettings{
		Name:     "hidden",
		Image:    whoamiImage,
		Host:     "hidden.localhost",
		Exposure: docker.ExposureLocal,
	})

	// A tailnet deploy before enable fails with the pointer to the fix.
	blocked := docker.NewApplication(ns, docker.ApplicationSettings{
		Name:     "whoami",
		Image:    whoamiImage,
		Host:     "whoami",
		Exposure: docker.ExposureTailnet,
	})
	require.ErrorIs(t, blocked.Deploy(ctx, nil), docker.ErrTailscaleNotEnabled)

	warnings, err := ns.EnableTailscale(ctx, controlSeamSettings(), nil)
	require.NoError(t, err)
	require.NotEmpty(t, warnings)
	assert.Contains(t, warnings[0], "--mode host", "defaulting to sidecar over a host socket must lead the warnings with the notice")

	docktailName := ns.Name() + "-docktail"
	sidecarName := ns.Name() + "-tailscale"

	// Settings live in the once label on once-docktail, recording sidecar mode.
	info, err := cli.ContainerInspect(ctx, docktailName)
	require.NoError(t, err)
	settings, err := docker.UnmarshalTailscaleSettings(info.Config.Labels["once"])
	require.NoError(t, err)
	assert.Equal(t, docker.TailscaleModeSidecar, settings.Mode)
	// Under the control seam the OAuth credentials are unused, so the enable
	// lifecycle drops them rather than storing what was passed in.
	assert.Empty(t, settings.ClientID)
	assert.Empty(t, settings.ClientSecret)
	assert.Contains(t, info.HostConfig.Binds, "/var/run/docker.sock:/var/run/docker.sock:ro")

	// The sidecar runs unprivileged userspace networking: no TUN device, no
	// NET_ADMIN, no host networking.
	sidecar, err := cli.ContainerInspect(ctx, sidecarName)
	require.NoError(t, err)
	assert.Empty(t, sidecar.HostConfig.CapAdd)
	assert.Empty(t, sidecar.HostConfig.Devices)
	assert.False(t, sidecar.HostConfig.NetworkMode.IsHost())
	assert.Contains(t, sidecar.Config.Env, "TS_USERSPACE=true")

	// The sidecar joins the control plane under its container name.
	require.Eventually(t, func() bool {
		found, online, err := cp.nodeOnline(ctx, sidecarName)
		return err == nil && found && online
	}, 90*time.Second, 2*time.Second, "sidecar never joined the control plane")

	// The Magic DNS suffix is read from the local daemon and persisted, so URL
	// display needs no live lookup. Enable itself only waits briefly, so allow
	// the lazy persist path to finish the job here.
	require.Eventually(t, func() bool {
		suffix, err := ns.Tailscale().DomainSuffix(ctx)
		return err == nil && suffix == magicDNSBaseDom
	}, 90*time.Second, 2*time.Second, "Magic DNS suffix never learned from the local daemon")
	persisted, err := ns.Tailscale().LoadSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, magicDNSBaseDom, persisted.DomainSuffix, "suffix should be persisted on the container label")

	// A stored label carrying no mode (a corrupt or legacy install) self-heals on
	// any re-enable: the mode resolves to the sidecar, which differs from the
	// stored one, so the topology is rebuilt with the mode stamped in - and a
	// re-enable is not a fresh enable, so it earns no --mode host notice.
	plantModelessDocktail(t, ctx, cli, docktailName, persisted)
	warnings, err = ns.EnableTailscale(ctx, controlSeamSettings(), nil)
	require.NoError(t, err)
	for _, warning := range warnings {
		assert.NotContains(t, warning, "--mode host")
	}
	healed, err := ns.Tailscale().LoadSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, docker.TailscaleModeSidecar, healed.Mode, "the rebuilt label must record the resolved mode")

	// With a mode stored, an unset mode keeps it: the re-enable is sticky, so
	// nothing is rebuilt.
	rebuilt, err := cli.ContainerInspect(ctx, docktailName)
	require.NoError(t, err)
	_, err = ns.EnableTailscale(ctx, controlSeamSettings(), nil)
	require.NoError(t, err)
	sticky, err := cli.ContainerInspect(ctx, docktailName)
	require.NoError(t, err)
	assert.Equal(t, rebuilt.ID, sticky.ID, "an unset mode must keep the stored one without a rebuild")

	// A stored label carrying no topology at all (a pre-ADR-0015 install)
	// refuses every read path with the older-Once error, and self-heals on a
	// re-enable exactly like a missing mode does - back to the topology the
	// re-enable explicitly asked for (Services, on this branch: ticket 03
	// ships only that adapter).
	plantTopologyLessDocktail(t, ctx, cli, docktailName, healed)
	_, err = ns.Tailscale().LoadSettings(ctx)
	require.ErrorContains(t, err, "older version of Once")
	_, err = ns.EnableTailscale(ctx, controlSeamSettings(), nil)
	require.NoError(t, err)
	rehealed, err := ns.Tailscale().LoadSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, docker.TailnetTopologyServices, rehealed.Topology, "the rebuilt label must record the resolved topology")

	// `deploy --expose tailnet` works now that Tailscale is up: the app is
	// advertised as a Service named from the App Name (its Host); the local
	// app stays off the tailnet.
	require.NoError(t, runCLI("-n", nsName, "deploy", whoamiImage, "--expose", "tailnet"))
	require.NoError(t, ns.Refresh(ctx))
	whoami := ns.ApplicationByHost("whoami")
	require.NotNil(t, whoami, "expected the tailnet app at the name-derived service name")
	whoamiName := whoami.Settings.Name
	assertAppLabels(t, ctx, cli, ns, whoamiName, hasDocktailLabels("whoami", ns.Name()))
	assertAppLabels(t, ctx, cli, ns, "hidden", noDocktailLabels)

	// Routing exclusivity: the local app routes through the proxy, the
	// tailnet app registers no proxy route.
	routes := proxyRoutes()
	assert.Contains(t, routes, "hidden")
	assert.NotContains(t, routes, "whoami")

	stateVolume := ns.Name() + "-tailscale-state"
	_, err = cli.VolumeInspect(ctx, stateVolume)
	require.NoError(t, err, "sidecar state volume should exist after enable")

	// Funnel is only available to tailnet-exposed apps.
	require.NoError(t, ns.Refresh(ctx))
	require.ErrorIs(t, ns.Application("hidden").EnableFunnel(ctx, time.Now().Add(time.Hour)), docker.ErrFunnelRequiresTailnet)

	// Funnel enable rolls the app with docktail.funnel.* labels, writing the
	// expiry into the once label in the same atomic recreation; disable strips
	// the funnel labels again while the service labels stay.
	expiresAt := time.Now().Add(time.Hour)
	require.NoError(t, ns.Application(whoamiName).EnableFunnel(ctx, expiresAt))
	assertAppLabels(t, ctx, cli, ns, whoamiName, func(t *testing.T, labels map[string]string) {
		hasDocktailLabels("whoami", ns.Name())(t, labels)
		assert.Equal(t, "true", labels["docktail.funnel.enable"])
		assert.Equal(t, "80", labels["docktail.funnel.port"])
		assert.Equal(t, "443", labels["docktail.funnel.funnel-port"])
		appSettings, err := docker.UnmarshalApplicationSettings(labels["once"])
		require.NoError(t, err)
		require.NotNil(t, appSettings.FunnelExpiresAt)
		assert.WithinDuration(t, expiresAt, *appSettings.FunnelExpiresAt, time.Second)
		assert.Equal(t, 443, appSettings.FunnelPort)
	})

	require.NoError(t, ns.Refresh(ctx))
	require.NoError(t, ns.Application(whoamiName).DisableFunnel(ctx))
	assertAppLabels(t, ctx, cli, ns, whoamiName, func(t *testing.T, labels map[string]string) {
		hasDocktailLabels("whoami", ns.Name())(t, labels)
		assert.NotContains(t, labels, "docktail.funnel.enable")
		assert.NotContains(t, labels, "docktail.funnel.port")
		assert.NotContains(t, labels, "docktail.funnel.funnel-port")
		appSettings, err := docker.UnmarshalApplicationSettings(labels["once"])
		require.NoError(t, err)
		assert.Nil(t, appSettings.FunnelExpiresAt)
	})

	// `update --expose` moves the app cleanly between routing paths: to local
	// (labels gone, proxy route registered, host re-derived with .localhost)
	// and back to tailnet (labels back, proxy route torn down). A live Funnel
	// does not survive leaving the tailnet - the expiry is cleared, so the
	// return trip cannot silently re-publish the app.
	require.NoError(t, ns.Refresh(ctx))
	require.NoError(t, ns.Application(whoamiName).EnableFunnel(ctx, time.Now().Add(time.Hour)))
	require.NoError(t, runCLI("-n", nsName, "update", "whoami", "--expose", "local"))
	assertAppLabels(t, ctx, cli, ns, whoamiName, func(t *testing.T, labels map[string]string) {
		noDocktailLabels(t, labels)
		appSettings, err := docker.UnmarshalApplicationSettings(labels["once"])
		require.NoError(t, err)
		assert.Nil(t, appSettings.FunnelExpiresAt, "leaving the tailnet must revoke the Funnel")
	})
	assert.Contains(t, proxyRoutes(), "whoami.localhost")

	require.NoError(t, runCLI("-n", nsName, "update", "whoami.localhost", "--expose", "tailnet"))
	assertAppLabels(t, ctx, cli, ns, whoamiName, func(t *testing.T, labels map[string]string) {
		hasDocktailLabels("whoami", ns.Name())(t, labels)
		assert.NotContains(t, labels, "docktail.funnel.enable")
	})
	assert.NotContains(t, proxyRoutes(), "whoami")

	// Disable removes docktail and the sidecar, keeps the state volume, and
	// rolls the apps to strip the labels.
	require.NoError(t, ns.Tailscale().Disable(ctx))

	_, err = cli.ContainerInspect(ctx, docktailName)
	assert.True(t, errdefs.IsNotFound(err), "docktail container should be gone after disable")
	_, err = cli.ContainerInspect(ctx, sidecarName)
	assert.True(t, errdefs.IsNotFound(err), "sidecar container should be gone after disable")
	_, err = cli.VolumeInspect(ctx, stateVolume)
	assert.NoError(t, err, "state volume should be retained after disable")
	_, err = cli.VolumeInspect(ctx, ns.Name()+"-tailscale-socket")
	assert.True(t, errdefs.IsNotFound(err), "socket volume should be removed with the topology")

	assertAppLabels(t, ctx, cli, ns, whoamiName, noDocktailLabels)
}

// TestTailscaleEnableUsesHostTailscaled proves host mode and the mode
// lifecycle: host mode boots only once-docktail (no sidecar) with the host
// socket directory mounted, re-enabling with the same mode keeps the topology
// as it is, and a differing mode rebuilds it like changed credentials do.
func TestTailscaleEnableUsesHostTailscaled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	ns, err := docker.NewNamespace("once-ts-host-test")
	require.NoError(t, err)
	require.NoError(t, ns.EnsureNetwork(ctx))
	t.Cleanup(func() { ns.Teardown(context.Background(), true) })

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	t.Cleanup(func() { cli.Close() })

	// The suffix read degrades gracefully (no host tailscale CLI here) and is
	// filled lazily later. The control seam env keeps the sidecar boot at the
	// mode change below off OAuth; no control plane is needed for topology
	// assertions.
	hostDir := plantHostTailscaledSocket(t)
	t.Setenv("ONCE_TAILSCALE_CONTROL_URL", "http://invalid.control.test:8080")
	t.Setenv("ONCE_TAILSCALE_AUTH_KEY", "dummy-auth-key")

	hostSettings := controlSeamSettings()
	hostSettings.Mode = docker.TailscaleModeHost
	require.NoError(t, ns.Tailscale().Enable(ctx, hostSettings))

	info, err := cli.ContainerInspect(ctx, ns.Name()+"-docktail")
	require.NoError(t, err)
	settings, err := docker.UnmarshalTailscaleSettings(info.Config.Labels["once"])
	require.NoError(t, err)
	assert.Equal(t, docker.TailscaleModeHost, settings.Mode)
	assert.Contains(t, info.HostConfig.Binds, hostDir+":/var/run/tailscale")

	_, err = cli.ContainerInspect(ctx, ns.Name()+"-tailscale")
	assert.True(t, errdefs.IsNotFound(err), "no sidecar should boot in host mode")

	// Re-enable with the same resolved mode is sticky: no rebuild. (Resolving an
	// unset mode to the stored one happens above Enable now, in EnableTailscale.)
	require.NoError(t, ns.Tailscale().Enable(ctx, hostSettings))
	again, err := cli.ContainerInspect(ctx, ns.Name()+"-docktail")
	require.NoError(t, err)
	assert.Equal(t, info.ID, again.ID, "sticky re-enable must not recreate docktail")
	settings, err = docker.UnmarshalTailscaleSettings(again.Config.Labels["once"])
	require.NoError(t, err)
	assert.Equal(t, docker.TailscaleModeHost, settings.Mode)

	// An explicit differing mode rebuilds the topology (state volume kept, as
	// on the changed-credentials path) and boots the sidecar this time.
	sidecarSettings := controlSeamSettings()
	sidecarSettings.Mode = docker.TailscaleModeSidecar
	require.NoError(t, ns.Tailscale().Enable(ctx, sidecarSettings))
	rebuilt, err := cli.ContainerInspect(ctx, ns.Name()+"-docktail")
	require.NoError(t, err)
	assert.NotEqual(t, info.ID, rebuilt.ID, "a mode change must rebuild docktail")
	settings, err = docker.UnmarshalTailscaleSettings(rebuilt.Config.Labels["once"])
	require.NoError(t, err)
	assert.Equal(t, docker.TailscaleModeSidecar, settings.Mode)
	_, err = cli.ContainerInspect(ctx, ns.Name()+"-tailscale")
	require.NoError(t, err, "the sidecar should boot after switching to sidecar mode")

	require.NoError(t, ns.Tailscale().Disable(ctx))
	_, err = cli.ContainerInspect(ctx, ns.Name()+"-docktail")
	assert.True(t, errdefs.IsNotFound(err), "docktail container should be gone after disable")
}

// TestTeardownRemovesTailscaleSystemResources proves the full-cleanup path:
// after enabling Tailscale and booting once-admin, teardown removes the
// Docktail topology AND deletes the sidecar's state volume (unlike disable,
// which retains it). No control plane is needed - cleanup doesn't depend on
// the sidecar joining a tailnet.
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

	// Point the control seam at a dummy so Enable creates the containers without
	// attempting OAuth.
	t.Setenv("ONCE_TAILSCALE_CONTROL_URL", "http://invalid.control.test:8080")
	t.Setenv("ONCE_TAILSCALE_AUTH_KEY", "dummy-auth-key")
	hideHostTailscaled(t)
	startAdminServer(t, ctx)

	sidecarSettings := controlSeamSettings()
	sidecarSettings.Mode = docker.TailscaleModeSidecar
	require.NoError(t, ns.Tailscale().Enable(ctx, sidecarSettings))
	require.NoError(t, ns.Admin().Boot(ctx))

	docktailName := ns.Name() + "-docktail"
	sidecarName := ns.Name() + "-tailscale"
	adminName := ns.Name() + "-admin"
	stateVolume := ns.Name() + "-tailscale-state"

	_, err = cli.ContainerInspect(ctx, docktailName)
	require.NoError(t, err, "docktail container should exist after enable")
	_, err = cli.ContainerInspect(ctx, sidecarName)
	require.NoError(t, err, "sidecar container should exist after enable")
	_, err = cli.ContainerInspect(ctx, adminName)
	require.NoError(t, err, "admin container should exist after boot")
	_, err = cli.VolumeInspect(ctx, stateVolume)
	require.NoError(t, err, "state volume should exist after enable")

	require.NoError(t, ns.Teardown(ctx, true))

	_, err = cli.ContainerInspect(ctx, docktailName)
	assert.True(t, errdefs.IsNotFound(err), "docktail container should be gone after teardown")
	_, err = cli.ContainerInspect(ctx, sidecarName)
	assert.True(t, errdefs.IsNotFound(err), "sidecar container should be gone after teardown")
	_, err = cli.ContainerInspect(ctx, adminName)
	assert.True(t, errdefs.IsNotFound(err), "admin container should be gone after teardown")
	_, err = cli.VolumeInspect(ctx, stateVolume)
	assert.True(t, errdefs.IsNotFound(err), "state volume should be deleted by teardown")
}

// TestTeardownNoopWhenTailscaleNeverEnabled proves teardown is safe and
// idempotent for the Tailscale resources when Tailscale was never enabled: no
// docktail/sidecar/admin containers or volumes exist, and teardown still
// succeeds.
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

	_, err = cli.ContainerInspect(ctx, ns.Name()+"-docktail")
	assert.True(t, errdefs.IsNotFound(err), "docktail container should be absent")
	_, err = cli.ContainerInspect(ctx, ns.Name()+"-tailscale")
	assert.True(t, errdefs.IsNotFound(err), "sidecar container should be absent")
	_, err = cli.ContainerInspect(ctx, ns.Name()+"-admin")
	assert.True(t, errdefs.IsNotFound(err), "admin container should be absent")
	_, err = cli.VolumeInspect(ctx, ns.Name()+"-tailscale-state")
	assert.True(t, errdefs.IsNotFound(err), "state volume should be absent")
}

func TestWhoamiDeploysViaHarness(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ns, err := docker.NewNamespace("once-whoami-test")
	require.NoError(t, err)
	defer ns.Teardown(ctx, true)

	require.NoError(t, ns.EnsureNetwork(ctx))
	bootProxy(t, ctx, ns, getProxyPorts(t))

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

// assertAppLabels inspects the named app's current container and checks its
// labels with the given predicate. The once label must always survive.
func assertAppLabels(t *testing.T, ctx context.Context, cli *client.Client, ns *docker.Namespace, appName string, check func(*testing.T, map[string]string)) {
	t.Helper()
	require.NoError(t, ns.Refresh(ctx))
	name, err := ns.Application(appName).ContainerName(ctx)
	require.NoError(t, err)
	info, err := cli.ContainerInspect(ctx, name)
	require.NoError(t, err)
	assert.NotEmpty(t, info.Config.Labels["once"], "once label must survive the roll")
	check(t, info.Config.Labels)
}

func hasDocktailLabels(appName, network string) func(*testing.T, map[string]string) {
	return func(t *testing.T, labels map[string]string) {
		t.Helper()
		assert.Equal(t, "true", labels["docktail.service.enable"])
		assert.Equal(t, appName, labels["docktail.service.name"])
		assert.Equal(t, "80", labels["docktail.service.port"])
		assert.Equal(t, "https", labels["docktail.service.service-protocol"])
		assert.Equal(t, network, labels["docktail.service.network"])
	}
}

func noDocktailLabels(t *testing.T, labels map[string]string) {
	t.Helper()
	assert.NotContains(t, labels, "docktail.service.enable")
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
