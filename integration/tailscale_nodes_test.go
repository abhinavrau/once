package integration

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
	"github.com/basecamp/once/internal/metrics"
)

// TestTailscaleEnableBootsNodesTopology drives the nodes topology end to end
// against headscale: a fresh enable with no requested topology defaults to
// nodes at the facade (ticket 03's resolveTopology - the CLI/TUI still pin
// services explicitly until ticket 05, so this exercises the facade
// directly), booting once-tsdproxy and no docktail/sidecar. once-admin joins
// under its own node; a tailnet-exposed app's tsdproxy.* labels target
// once-proxy, its own node joins under its Host, and the Proxy carries a
// route on {app}.{suffix} with TLS off. Under headscale there is no
// `tailscale cert`, so the request that proves the node -> Proxy path is
// plain HTTP shaped exactly like TSDProxy's own forward (Host preserved) -
// TSDProxy's own tsnet reachability from a real tailnet peer is its
// well-established behaviour, not this test's to reprove. kamal-proxy's own
// Prometheus metrics - the dashboard Traffic card's source - show the
// request against the app's service name, proving tailnet apps are visible
// to it for free under nodes (ADR-0015). Disable removes once-tsdproxy and
// rolls apps to strip the labels.
func TestTailscaleEnableBootsNodesTopology(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	nsName := "once-ts-nodes-test"
	ns, err := docker.NewNamespace(nsName)
	require.NoError(t, err)
	require.NoError(t, ns.EnsureNetwork(ctx))
	proxyPorts := getProxyPorts(t)
	bootProxy(t, ctx, ns, proxyPorts)
	t.Cleanup(func() { ns.Teardown(context.Background(), true) })

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	t.Cleanup(func() { cli.Close() })

	cp := newControlPlane(t, ctx, ns)
	useControlSeam(t, ctx, cp)
	hideHostTailscaled(t)
	startAdminServer(t, ctx)

	tsdproxyName := ns.Name() + "-tsdproxy"
	docktailName := ns.Name() + "-docktail"
	sidecarName := ns.Name() + "-tailscale"

	// A tailnet deploy before enable fails with the pointer to the fix, the
	// same guarantee under nodes as under services.
	blocked := docker.NewApplication(ns, docker.ApplicationSettings{
		Name: "blocked", Image: whoamiImage, Host: "blocked", Exposure: docker.ExposureTailnet,
	})
	require.ErrorIs(t, blocked.Deploy(ctx, nil), docker.ErrTailscaleNotEnabled)

	// No requested topology: resolveTopology defaults a fresh enable to nodes.
	_, err = ns.EnableTailscale(ctx, docker.TailscaleSettings{
		ClientID:     "unused-with-control-seam",
		ClientSecret: "unused-with-control-seam",
	}, nil)
	require.NoError(t, err)

	info, err := cli.ContainerInspect(ctx, tsdproxyName)
	require.NoError(t, err)
	assert.True(t, info.State.Running)
	settings, err := docker.UnmarshalTailscaleSettings(info.Config.Labels["once"])
	require.NoError(t, err)
	assert.Equal(t, docker.TailnetTopologyNodes, settings.Topology)
	assert.Contains(t, info.HostConfig.Binds, "/var/run/docker.sock:/var/run/docker.sock:ro")

	_, err = cli.ContainerInspect(ctx, docktailName)
	assert.True(t, errdefs.IsNotFound(err), "no docktail container under nodes")
	_, err = cli.ContainerInspect(ctx, sidecarName)
	assert.True(t, errdefs.IsNotFound(err), "no tailscale sidecar under nodes")

	// once-admin is exposed the same way an app is: its own TSDProxy node.
	require.Eventually(t, func() bool {
		found, online, err := cp.nodeOnline(ctx, "once-admin")
		return err == nil && found && online
	}, 90*time.Second, 2*time.Second, "once-admin's tsdproxy node never joined the control plane")

	require.Eventually(t, func() bool {
		suffix, err := ns.Tailscale().DomainSuffix(ctx)
		return err == nil && suffix == magicDNSBaseDom
	}, 30*time.Second, time.Second, "Magic DNS suffix never learned from once-admin's node")

	whoami := deployApp(t, ctx, ns, docker.ApplicationSettings{
		Name: "whoami", Image: whoamiImage, Host: "whoami", Exposure: docker.ExposureTailnet,
	})
	require.NotNil(t, whoami)
	assertAppLabels(t, ctx, cli, ns, "whoami", hasNodesLabels)

	// The app's own node joins headscale under its Host (unlike Services'
	// shared sidecar, every app is its own device here).
	require.Eventually(t, func() bool {
		found, online, err := cp.nodeOnline(ctx, "whoami")
		return err == nil && found && online
	}, 90*time.Second, 2*time.Second, "whoami's tsdproxy node never joined the control plane")

	routes := func() string {
		out, err := ns.Proxy().ExecOutput(ctx, []string{"kamal-proxy", "list"})
		require.NoError(t, err)
		return out
	}
	wantHost := "whoami." + magicDNSBaseDom
	require.Eventually(t, func() bool {
		return strings.Contains(routes(), wantHost)
	}, 30*time.Second, time.Second, "whoami's proxy route on its tailnet host never appeared")
	assert.NotContains(t, routes(), "--tls", "a nodes route must not ask kamal-proxy for its own TLS - TSDProxy already terminated it")

	// Funnel enable adds the tailscale_funnel port option and needs no port
	// allocation (funnelPort is a fixed constant under nodes); disable
	// removes it again. Headscale has no Funnel support at all, so this only
	// proves the label mechanics - the same scope the existing Services test
	// covers via Application.EnableFunnel, without Namespace.EnableFunnel's
	// live activation wait. Both calls Roll the app, which is also this
	// test's proof that a Roll never touches once-tsdproxy: the target is
	// always the Proxy, so nothing about the app container changing needs
	// the topology adapter involved.
	tsdproxyBefore, err := cli.ContainerInspect(ctx, tsdproxyName)
	require.NoError(t, err)

	require.NoError(t, ns.Refresh(ctx))
	require.NoError(t, ns.Application("whoami").EnableFunnel(ctx, time.Now().Add(time.Hour)))
	assertAppLabels(t, ctx, cli, ns, "whoami", func(t *testing.T, labels map[string]string) {
		hasNodesLabels(t, labels)
		assert.Contains(t, labels["tsdproxy.port.1"], "tailscale_funnel")
	})
	require.NoError(t, ns.Refresh(ctx))
	require.NoError(t, ns.Application("whoami").DisableFunnel(ctx))
	assertAppLabels(t, ctx, cli, ns, "whoami", func(t *testing.T, labels map[string]string) {
		hasNodesLabels(t, labels)
		assert.NotContains(t, labels["tsdproxy.port.1"], "tailscale_funnel")
	})

	tsdproxyAfter, err := cli.ContainerInspect(ctx, tsdproxyName)
	require.NoError(t, err)
	assert.Equal(t, tsdproxyBefore.ID, tsdproxyAfter.ID, "rolling an app must not recreate once-tsdproxy")
	assert.Equal(t, tsdproxyBefore.State.StartedAt, tsdproxyAfter.State.StartedAt, "rolling an app must not restart once-tsdproxy")

	// HTTP reaches the app via the Proxy: a request shaped exactly like
	// TSDProxy's own forward (Host preserved, plain HTTP - headscale has no
	// `tailscale cert`) is routed to the app container.
	requestApp := func() (int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/", proxyPorts.HTTPPort), nil)
		if err != nil {
			return 0, err
		}
		req.Host = wantHost
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err
		}
		resp.Body.Close()
		return resp.StatusCode, nil
	}
	status, err := requestApp()
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)

	// The dashboard's Traffic card reads exactly this: kamal-proxy's own
	// Prometheus metrics, keyed by the app's service name - proving tailnet
	// apps under nodes are visible to it for free (ADR-0015), unlike
	// services (which route straight to the container). The counter's
	// service series does not exist before the first request above, so the
	// scraper's first-ever sight of it always reads as a zero delta (nothing
	// to diff against yet); requesting again inside the wait, not just
	// scraping again, is what gives it a second data point to diff.
	scraper := metrics.NewMetricsScraper(metrics.ScraperSettings{Port: proxyPorts.MetricsPort})
	require.Eventually(t, func() bool {
		if _, err := requestApp(); err != nil {
			return false
		}
		scraper.Scrape(ctx)
		for _, sample := range scraper.Fetch("whoami", 5) {
			if sample.Success > 0 {
				return true
			}
		}
		return false
	}, 20*time.Second, time.Second, "kamal-proxy never reported a request for whoami")

	// Disable removes once-tsdproxy, keeps its data volume, and rolls the
	// apps to strip the labels.
	require.NoError(t, ns.Tailscale().Disable(ctx))

	_, err = cli.ContainerInspect(ctx, tsdproxyName)
	assert.True(t, errdefs.IsNotFound(err), "tsdproxy container should be gone after disable")
	_, err = cli.VolumeInspect(ctx, ns.Name()+"-tsdproxy-data")
	assert.NoError(t, err, "data volume should be retained after disable")
	assertAppLabels(t, ctx, cli, ns, "whoami", noNodesLabels)
}

func hasNodesLabels(t *testing.T, labels map[string]string) {
	t.Helper()
	assert.Equal(t, "true", labels["tsdproxy.enable"])
	assert.Contains(t, labels["tsdproxy.port.1"], "80/http")
	assert.Contains(t, labels["tsdproxy.port.1"], "no_autodetect")
}

func noNodesLabels(t *testing.T, labels map[string]string) {
	t.Helper()
	assert.NotContains(t, labels, "tsdproxy.enable")
}

// TestTailscaleRoundTripsBetweenNodesAndServices proves both directions of
// the topology-switch path ticket 03 could only exercise through the
// mode-change/self-heal rebuild (it shipped with no second adapter yet): an
// explicit --topology request against a live install of the other topology
// removes the old topology's containers (nodes keeps its data volume -
// ADR-0015's "leave nodes" doctrine, not a full teardown), boots the new
// one, and rolls the app onto its labels.
func TestTailscaleRoundTripsBetweenNodesAndServices(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	nsName := "once-ts-switch-test"
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
	hideHostTailscaled(t)
	startAdminServer(t, ctx)

	tsdproxyName := ns.Name() + "-tsdproxy"
	docktailName := ns.Name() + "-docktail"
	sidecarName := ns.Name() + "-tailscale"
	dataVolume := ns.Name() + "-tsdproxy-data"

	_, err = ns.EnableTailscale(ctx, docker.TailscaleSettings{
		ClientID: "unused-with-control-seam", ClientSecret: "unused-with-control-seam",
		Topology: docker.TailnetTopologyNodes,
	}, nil)
	require.NoError(t, err)

	whoami := deployApp(t, ctx, ns, docker.ApplicationSettings{
		Name: "whoami", Image: whoamiImage, Host: "whoami", Exposure: docker.ExposureTailnet,
	})
	require.NotNil(t, whoami)
	assertAppLabels(t, ctx, cli, ns, "whoami", hasNodesLabels)

	_, err = cli.VolumeInspect(ctx, dataVolume)
	require.NoError(t, err, "the data volume must exist before the switch, or its retention proves nothing")

	// Switch to services: once-tsdproxy is torn down, docktail/the sidecar
	// come up, and the app is rolled with docktail labels.
	_, err = ns.EnableTailscale(ctx, docker.TailscaleSettings{
		ClientID: "unused-with-control-seam", ClientSecret: "unused-with-control-seam",
		Topology: docker.TailnetTopologyServices,
	}, nil)
	require.NoError(t, err)

	_, err = cli.ContainerInspect(ctx, tsdproxyName)
	assert.True(t, errdefs.IsNotFound(err), "tsdproxy container should be gone after switching to services")
	_, err = cli.VolumeInspect(ctx, dataVolume)
	assert.NoError(t, err, "leaving nodes keeps its data volume, like every other topology removal")

	info, err := cli.ContainerInspect(ctx, docktailName)
	require.NoError(t, err, "docktail should exist after switching to services")
	assert.True(t, info.State.Running)
	_, err = cli.ContainerInspect(ctx, sidecarName)
	require.NoError(t, err, "the sidecar should exist after switching to services (sidecar is the default mode)")

	assertAppLabels(t, ctx, cli, ns, "whoami", func(t *testing.T, labels map[string]string) {
		assert.NotContains(t, labels, "tsdproxy.enable", "the app must be rolled off nodes' labels")
		assert.Equal(t, "true", labels["docktail.service.enable"], "the app must be rolled onto services' labels")
	})

	// Switch back to nodes: docktail and the sidecar are torn down, and the
	// data volume kept above lets the app node rejoin without churn.
	_, err = ns.EnableTailscale(ctx, docker.TailscaleSettings{
		ClientID: "unused-with-control-seam", ClientSecret: "unused-with-control-seam",
		Topology: docker.TailnetTopologyNodes,
	}, nil)
	require.NoError(t, err)

	_, err = cli.ContainerInspect(ctx, docktailName)
	assert.True(t, errdefs.IsNotFound(err), "docktail container should be gone after switching back to nodes")
	_, err = cli.ContainerInspect(ctx, sidecarName)
	assert.True(t, errdefs.IsNotFound(err), "the sidecar should be gone after switching back to nodes")

	info, err = cli.ContainerInspect(ctx, tsdproxyName)
	require.NoError(t, err, "tsdproxy should exist again after switching back to nodes")
	assert.True(t, info.State.Running)

	assertAppLabels(t, ctx, cli, ns, "whoami", func(t *testing.T, labels map[string]string) {
		assert.NotContains(t, labels, "docktail.service.enable", "the app must be rolled off services' labels")
		hasNodesLabels(t, labels)
	})
}

// A fresh install has never deployed an app, so no proxy exists yet. Under
// nodes, TSDProxy forwards every tailnet Host to the Proxy, so the enable
// lifecycle must boot it before once-admin registers its route - exactly what
// a first-run TUI user hits.
func TestTailscaleEnableNodesOnFreshInstallBootsProxy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ns, err := docker.NewNamespace("once-ts-nodes-fresh-test")
	require.NoError(t, err)
	require.NoError(t, ns.EnsureNetwork(ctx))
	t.Cleanup(func() { ns.Teardown(context.Background(), true) })

	cp := newControlPlane(t, ctx, ns)
	useControlSeam(t, ctx, cp)
	hideHostTailscaled(t)
	startAdminServer(t, ctx)

	_, err = ns.EnableTailscale(ctx, docker.TailscaleSettings{
		ClientID:     "unused-with-control-seam",
		ClientSecret: "unused-with-control-seam",
		Topology:     docker.TailnetTopologyNodes,
	}, nil)
	require.NoError(t, err)

	assertContainerRunning(t, ctx, ns.Name()+"-proxy", true)
	assertContainerRunning(t, ctx, ns.Name()+"-admin", true)

	// once-admin is the first node to join a fresh install, so its Proxy route
	// is registered while the tailnet still reports no Magic DNS suffix. That
	// race used to register a bare Host, which no request TSDProxy forwards
	// ever matches - a healthy container answering 404 forever.
	out, err := ns.Proxy().ExecOutput(ctx, []string{"kamal-proxy", "list"})
	require.NoError(t, err)
	assert.Contains(t, out, "once-admin."+magicDNSBaseDom)
}

// TestTailscaleNodesNamespacesOnOneHostServeOnlyTheirOwnApps runs two nodes
// installs side by side, each on its own control plane, with the same app and
// once-admin names in both - the shape that let one namespace's TSDProxy
// serve, and then stop, the other's nodes, because every TSDProxy watches
// every container on the host (ADR-0018). Each must serve exactly its own
// containers, and tearing one install down must leave the other's proxies
// running on the same containers.
func TestTailscaleNodesNamespacesOnOneHostServeOnlyTheirOwnApps(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	t.Cleanup(func() { cli.Close() })

	hideHostTailscaled(t)
	startAdminServer(t, ctx)

	bootNodesInstall := func(name, ownApp string) *docker.Namespace {
		ns, err := docker.NewNamespace(name)
		require.NoError(t, err)
		require.NoError(t, ns.EnsureNetwork(ctx))
		bootProxy(t, ctx, ns, getProxyPorts(t))
		t.Cleanup(func() { ns.Teardown(context.Background(), true) })

		// The seam is read when each TSDProxy's config is written, so each
		// install joins the control plane on its own network.
		useControlSeam(t, ctx, newControlPlane(t, ctx, ns))
		_, err = ns.EnableTailscale(ctx, docker.TailscaleSettings{
			ClientID:     "unused-with-control-seam",
			ClientSecret: "unused-with-control-seam",
			Topology:     docker.TailnetTopologyNodes,
		}, nil)
		require.NoError(t, err)

		for _, app := range []string{"whoami", ownApp} {
			deployApp(t, ctx, ns, docker.ApplicationSettings{
				Name: app, Image: whoamiImage, Host: app, Exposure: docker.ExposureTailnet,
			})
			assertAppLabels(t, ctx, cli, ns, app, func(t *testing.T, labels map[string]string) {
				assert.Equal(t, ns.Name(), labels["tsdproxy.proxyprovider"])
			})
		}
		require.Eventually(t, func() bool {
			return servesRunning(tsdproxyProxies(t, ctx, cli, ns), "once-admin", "whoami", ownApp)
		}, 2*time.Minute, 2*time.Second, "%s's nodes never all ran", name)
		return ns
	}

	first := bootNodesInstall("once-ts-nodes-iso-a-test", "only-a")
	before := tsdproxyProxies(t, ctx, cli, first)
	second := bootNodesInstall("once-ts-nodes-iso-b-test", "only-b")

	assertServesOnly := func(ns *docker.Namespace, foreignApp string) {
		t.Helper()
		proxies := tsdproxyProxies(t, ctx, cli, ns)
		assert.NotContains(t, proxies, foreignApp)
		for name, proxy := range proxies {
			info, err := cli.ContainerInspect(ctx, proxy.TargetID)
			require.NoError(t, err)
			assert.True(t, strings.HasPrefix(strings.TrimPrefix(info.Name, "/"), ns.Name()+"-"),
				"%s's TSDProxy serves %s from container %s", ns.Name(), name, info.Name)
		}
	}
	assertServesOnly(first, "only-b")
	assertServesOnly(second, "only-a")

	assertUndisturbed := func() {
		t.Helper()
		after := tsdproxyProxies(t, ctx, cli, first)
		for _, name := range []string{"once-admin", "whoami", "only-a"} {
			assert.Equal(t, before[name].TargetID, after[name].TargetID, "%s moved to another container", name)
			assert.Equal(t, "Running", after[name].Status, "%s stopped", name)
		}
	}
	assertUndisturbed()

	// The control plane is the test's, not the install's, so Teardown leaves
	// it holding the network it would remove.
	require.NoError(t, cli.ContainerRemove(ctx, second.Name()+"-headscale", container.RemoveOptions{Force: true}))
	require.NoError(t, second.Teardown(ctx, true))
	// TSDProxy acts on a container's die event asynchronously; give a
	// hijacking stop the time it would need to land.
	time.Sleep(5 * time.Second)
	assertUndisturbed()
}

// TestTailscaleNodesHealKeepsLegacyNodeState stands in an install whose
// TSDProxy predates ADR-0018 - every node's state under the "default"
// provider, a config serving every unlabelled container - and has the daemon's
// startup heal rebuild it. Every node must rejoin under the name it already
// held: losing the state would register each one afresh, deduped to "-1".
// The legacy TSDProxy is only ever created, never started, so it cannot serve
// anything on this host meanwhile.
func TestTailscaleNodesHealKeepsLegacyNodeState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	nsName := "once-ts-nodes-heal-test"
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
	hideHostTailscaled(t)
	startAdminServer(t, ctx)

	_, err = ns.EnableTailscale(ctx, docker.TailscaleSettings{
		ClientID:     "unused-with-control-seam",
		ClientSecret: "unused-with-control-seam",
		Topology:     docker.TailnetTopologyNodes,
	}, nil)
	require.NoError(t, err)
	deployApp(t, ctx, ns, docker.ApplicationSettings{
		Name: "whoami", Image: whoamiImage, Host: "whoami", Exposure: docker.ExposureTailnet,
	})
	nodesOnline := func() bool {
		for _, name := range []string{"once-admin", "whoami"} {
			if found, online, err := cp.nodeOnline(ctx, name); err != nil || !found || !online {
				return false
			}
		}
		return true
	}
	require.Eventually(t, nodesOnline, 2*time.Minute, 2*time.Second, "nodes never joined")
	nodesBefore, err := cp.nodeNames(ctx)
	require.NoError(t, err)

	tsdproxyName := nsName + "-tsdproxy"
	dataVolume := nsName + "-tsdproxy-data"
	dataMount := []mount.Mount{{Type: mount.TypeVolume, Source: dataVolume, Target: "/data"}}
	current, err := cli.ContainerInspect(ctx, tsdproxyName)
	require.NoError(t, err)
	require.NoError(t, cli.ContainerRemove(ctx, current.ID, container.RemoveOptions{Force: true}))

	exitCode, output, err := runContainerToCompletion(ctx,
		&container.Config{Image: "nginx:1.27-alpine", Cmd: []string{"mv", "/data/" + nsName, "/data/default"}},
		&container.HostConfig{Mounts: dataMount},
	)
	require.NoError(t, err)
	require.Equal(t, 0, exitCode, output)

	legacy, err := cli.ContainerCreate(ctx,
		&container.Config{Image: current.Config.Image, Labels: current.Config.Labels},
		&container.HostConfig{Mounts: dataMount},
		nil, nil, tsdproxyName)
	require.NoError(t, err)
	copyFileToContainer(t, ctx, cli, legacy.ID, "config/tsdproxy.yaml", legacyTSDProxyConfig(nsName))

	require.NoError(t, ns.Tailscale().Heal(ctx))

	healed, err := cli.ContainerInspect(ctx, tsdproxyName)
	require.NoError(t, err)
	assert.NotEqual(t, legacy.ID, healed.ID)
	assert.True(t, healed.State.Running)

	stat, err := cli.ContainerStatPath(ctx, healed.ID, "/data/"+nsName)
	require.NoError(t, err)
	assert.NotZero(t, stat.Mode&os.ModeSymlink, "the provider's state directory must link to the legacy one")

	require.Eventually(t, nodesOnline, 2*time.Minute, 2*time.Second, "nodes never rejoined after the heal")
	nodesAfter, err := cp.nodeNames(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, nodesBefore, nodesAfter, "every node must rejoin under the name it held")

	// A second heal finds everything current and touches nothing.
	require.NoError(t, ns.Tailscale().Heal(ctx))
	again, err := cli.ContainerInspect(ctx, tsdproxyName)
	require.NoError(t, err)
	assert.Equal(t, healed.ID, again.ID)
}

// Helpers

// tsdproxyProxy is the slice of a TSDProxy /api/v1/proxies entry these tests
// read: which container a proxy serves, and whether it runs.
type tsdproxyProxy struct {
	Name     string `json:"name"`
	TargetID string `json:"targetId"`
	Status   string `json:"status"`
}

// tsdproxyProxies reads the namespace's TSDProxy admin API, keyed by proxy
// name.
func tsdproxyProxies(t *testing.T, ctx context.Context, cli *client.Client, ns *docker.Namespace) map[string]tsdproxyProxy {
	t.Helper()
	info, err := cli.ContainerInspect(ctx, ns.Name()+"-tsdproxy")
	require.NoError(t, err)
	bindings := info.NetworkSettings.Ports["8080/tcp"]
	require.NotEmpty(t, bindings)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+bindings[0].HostPort+"/api/v1/proxies", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var body struct {
		Proxies []tsdproxyProxy `json:"proxies"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	proxies := map[string]tsdproxyProxy{}
	for _, p := range body.Proxies {
		proxies[p.Name] = p
	}
	return proxies
}

func servesRunning(proxies map[string]tsdproxyProxy, names ...string) bool {
	for _, name := range names {
		if proxies[name].Status != "Running" {
			return false
		}
	}
	return true
}

// legacyTSDProxyConfig is the tsdproxy.yaml Once wrote before ADR-0018: one
// provider named "default", which both defaults name.
func legacyTSDProxyConfig(nsName string) string {
	return fmt.Sprintf(`defaultProxyProvider: default
docker:
  local:
    host: unix:///var/run/docker.sock
    targetHostname: %s-proxy
    defaultProxyProvider: default
tailscale:
  providers:
    default:
      authKey: %s
      controlUrl: %s
      tags: tag:once
  dataDir: /data/
http:
  hostname: 0.0.0.0
  port: 8080
adminAllowLocalhost: true
`, nsName, os.Getenv("ONCE_TAILSCALE_AUTH_KEY"), os.Getenv("ONCE_TAILSCALE_CONTROL_URL"))
}

func copyFileToContainer(t *testing.T, ctx context.Context, cli *client.Client, containerID, name, content string) {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(content))}))
	_, err := tw.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, cli.CopyToContainer(ctx, containerID, "/", &buf, container.CopyToContainerOptions{}))
}
