package integration

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/containerd/errdefs"
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
	require.NoError(t, ns.Proxy().Boot(ctx, proxyPorts))
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

// TestTailscaleSwitchesFromNodesToServices proves the topology-switch path
// ticket 03 could only exercise through the mode-change/self-heal rebuild
// (it shipped with no second adapter yet): an explicit --topology services
// request against a live nodes install removes once-tsdproxy (keeping its
// data volume - ADR-0015's "leave nodes" doctrine, not a full teardown),
// boots once-docktail and the sidecar, and rolls the app so it carries
// docktail labels instead of tsdproxy's.
func TestTailscaleSwitchesFromNodesToServices(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	nsName := "once-ts-switch-test"
	ns, err := docker.NewNamespace(nsName)
	require.NoError(t, err)
	require.NoError(t, ns.EnsureNetwork(ctx))
	require.NoError(t, ns.Proxy().Boot(ctx, getProxyPorts(t)))
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
}
