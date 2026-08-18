package docker

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUniqueName(t *testing.T) {
	ns := &Namespace{name: "test"}

	name, err := ns.UniqueName("myapp")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(name, "myapp-"))
	assert.Len(t, name, len("myapp-")+6)
	// The name becomes the docktail.service.name label, which must be a valid
	// RFC 1123 hostname - no dots (regression guard: a "." separator broke
	// tailnet registration).
	assert.NotContains(t, name, ".")

	name2, err := ns.UniqueName("myapp")
	require.NoError(t, err)
	assert.NotEqual(t, name, name2)
}

func TestDeployPreconditionsRunBeforeDockerWork(t *testing.T) {
	// The preconditions must run before any Docker work; the nil client would
	// panic in Setup if a check were reordered below it.
	ns := &Namespace{name: "test"}
	ns.addApplication(ApplicationSettings{Name: "taken", Host: "taken.localhost"})

	// Validation comes first: an invalid Configuration on a taken host reports
	// the validation error, not the host collision.
	settings := DefaultApplicationSettings()
	settings.Host = "taken.localhost"
	_, _, err := ns.Deploy(context.Background(), settings, nil)
	assert.ErrorIs(t, err, ErrImageRequired)

	settings.Profile.DataPath = "relative"
	_, _, err = ns.Deploy(context.Background(), settings, nil)
	assert.ErrorIs(t, err, ErrImageRequired)

	settings.Image = "image:latest"
	_, _, err = ns.Deploy(context.Background(), settings, nil)
	assert.ErrorIs(t, err, ErrDataPathInvalid)

	settings.Profile.DataPath = ""
	_, _, err = ns.Deploy(context.Background(), settings, nil)
	assert.ErrorIs(t, err, ErrHostnameInUse)
}

func TestEnableTailscaleFailsFastWithoutDaemon(t *testing.T) {
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", t.TempDir())

	ns := &Namespace{name: "test"}
	ns.admin = NewAdmin(ns)

	// The daemon precondition must run before any Docker work; a nil client would
	// panic in EnsureNetwork/tailscale.Enable if the check were reordered below it.
	_, err := ns.EnableTailscale(context.Background(), TailscaleSettings{ClientID: "id", ClientSecret: "secret"}, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "once-admin socket")
}

func TestEnableTailscaleHostModePreflightBeforeProbeAndContainers(t *testing.T) {
	t.Setenv("ONCE_ADMIN_RUNTIME_DIR", t.TempDir())
	ln, err := net.Listen("unix", AdminSocketPath())
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	require.NoError(t, os.WriteFile(AdminNginxConfPath(), []byte(AdminNginxConfig()), 0o644))
	t.Setenv("ONCE_TAILSCALED_DIR", t.TempDir())

	ns := &Namespace{name: "test"}
	ns.admin = NewAdmin(ns)
	// The topology adapter is constructed (no Docker touched by that alone),
	// but resolving *which* topology from an empty request would inspect the
	// provider containers - so the topology is given explicitly here to keep
	// this test's "before any Docker work" guarantee (ns.client stays nil).
	ns.tailscale = NewTailscale(ns)

	// With no host socket, --mode host must fail on the pre-flight before the
	// credential probe (the error is the socket one, not a credentials one) and
	// before any Docker work (a nil client would panic past that point).
	_, err = ns.EnableTailscale(context.Background(),
		TailscaleSettings{ClientID: "id", ClientSecret: "secret", Tag: "tag:once", Mode: TailscaleModeHost, Topology: TailnetTopologyServices}, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), tailscaledSocketFile)
	assert.Contains(t, err.Error(), "--mode sidecar")
}

func TestEnableFunnelRejectsInvalidDurationBeforePreconditions(t *testing.T) {
	// A nil tailscale would panic in the live enabled check, so an invalid
	// duration must be rejected before the Tailscale and daemon preconditions.
	ns := &Namespace{name: "test"}
	app := NewApplication(ns, ApplicationSettings{Name: "books", Exposure: ExposureTailnet})

	_, _, err := ns.EnableFunnel(context.Background(), app, -time.Minute)
	assert.ErrorIs(t, err, ErrFunnelDurationInvalid)

	_, _, err = ns.EnableFunnel(context.Background(), app, 25*time.Hour)
	assert.ErrorIs(t, err, ErrFunnelDurationTooLong)
}

func TestApplicationLookup(t *testing.T) {
	ns := &Namespace{name: "test"}
	ns.applications = append(ns.applications, NewApplication(ns, ApplicationSettings{Name: "alpha"}))
	ns.applications = append(ns.applications, NewApplication(ns, ApplicationSettings{Name: "beta"}))

	assert.NotNil(t, ns.Application("alpha"))
	assert.Equal(t, "alpha", ns.Application("alpha").Settings.Name)
	assert.NotNil(t, ns.Application("beta"))
	assert.Nil(t, ns.Application("missing"))
}

func TestApplicationByHost(t *testing.T) {
	ns := &Namespace{name: "test"}
	ns.applications = append(ns.applications,
		NewApplication(ns, ApplicationSettings{Name: "app1", Host: "app1.localhost"}),
		NewApplication(ns, ApplicationSettings{Name: "app2", Host: "app2.localhost"}),
	)

	app := ns.ApplicationByHost("app1.localhost")
	require.NotNil(t, app)
	assert.Equal(t, "app1", app.Settings.Name)

	app = ns.ApplicationByHost("app2.localhost")
	require.NotNil(t, app)
	assert.Equal(t, "app2", app.Settings.Name)

	assert.Nil(t, ns.ApplicationByHost("missing.localhost"))
}

func TestApplicationByNameOrHost(t *testing.T) {
	ns := &Namespace{name: "once"}
	ns.applications = append(ns.applications,
		NewApplication(ns, ApplicationSettings{Name: "once-campfire-73baaa", Host: "once-campfire", Exposure: ExposureTailnet}),
		NewApplication(ns, ApplicationSettings{Name: "once-campfire", Host: "other.localhost"}),
	)

	app := ns.ApplicationByNameOrHost("once-campfire-73baaa")
	require.NotNil(t, app)
	assert.Equal(t, "once-campfire-73baaa", app.Settings.Name)

	// name match wins over another app's host
	app = ns.ApplicationByNameOrHost("once-campfire")
	require.NotNil(t, app)
	assert.Equal(t, "once-campfire", app.Settings.Name)

	ns.applications = ns.applications[:1]
	app = ns.ApplicationByNameOrHost("once-campfire")
	require.NotNil(t, app)
	assert.Equal(t, "once-campfire-73baaa", app.Settings.Name)

	assert.Nil(t, ns.ApplicationByNameOrHost("missing"))
}

func TestAllocateFunnelPort(t *testing.T) {
	ns := &Namespace{name: "once"}
	newApp := func(name string) *Application {
		app := NewApplication(ns, ApplicationSettings{Name: name, Host: name, Exposure: ExposureTailnet})
		ns.applications = append(ns.applications, app)
		return app
	}
	activate := func(app *Application) int {
		t.Helper()
		port, err := (&servicesTopology{}).funnelPort(app, ns.applications)
		require.NoError(t, err)
		expires := time.Now().Add(time.Hour)
		app.Settings.FunnelExpiresAt = &expires
		app.Settings.FunnelPort = port
		return port
	}

	campfire, fizzy, writebook := newApp("campfire"), newApp("fizzy"), newApp("writebook")

	// Concurrent funnels each get their own public port instead of colliding
	// on the node's single 443 endpoint.
	assert.Equal(t, 443, activate(campfire))
	assert.Equal(t, 8443, activate(fizzy))
	assert.Equal(t, 10000, activate(writebook))

	// A re-enable keeps the app's live port; a fourth concurrent funnel is
	// rejected rather than colliding.
	port, err := (&servicesTopology{}).funnelPort(fizzy, ns.applications)
	require.NoError(t, err)
	assert.Equal(t, 8443, port)
	_, err = (&servicesTopology{}).funnelPort(newApp("fourth"), ns.applications)
	assert.ErrorIs(t, err, ErrFunnelPortsExhausted)

	// A pre-allocation label (funnel active, no port recorded) holds 443.
	ns2 := &Namespace{name: "once"}
	legacy := NewApplication(ns2, ApplicationSettings{Name: "legacy", Host: "legacy", Exposure: ExposureTailnet})
	expires := time.Now().Add(time.Hour)
	legacy.Settings.FunnelExpiresAt = &expires
	next := NewApplication(ns2, ApplicationSettings{Name: "next", Host: "next", Exposure: ExposureTailnet})
	ns2.applications = append(ns2.applications, legacy, next)
	port, err = (&servicesTopology{}).funnelPort(next, ns2.applications)
	require.NoError(t, err)
	assert.Equal(t, 8443, port)
}

func TestHostInUse(t *testing.T) {
	ns := &Namespace{name: "test"}
	ns.applications = append(ns.applications,
		NewApplication(ns, ApplicationSettings{Name: "app1", Host: "app1.localhost"}),
		NewApplication(ns, ApplicationSettings{Name: "app2", Host: "app2.localhost"}),
	)

	assert.True(t, ns.HostInUse("app1.localhost"))
	assert.True(t, ns.HostInUse("app2.localhost"))
	assert.False(t, ns.HostInUse("other.localhost"))
}

func TestHostInUseByAnother(t *testing.T) {
	ns := &Namespace{name: "test"}
	ns.applications = append(ns.applications,
		NewApplication(ns, ApplicationSettings{Name: "app1", Host: "shared.localhost"}),
		NewApplication(ns, ApplicationSettings{Name: "app2", Host: "unique.localhost"}),
	)

	assert.False(t, ns.HostInUseByAnother("shared.localhost", "app1"))
	assert.True(t, ns.HostInUseByAnother("shared.localhost", "app2"))
	assert.False(t, ns.HostInUseByAnother("other.localhost", "app1"))
}

func TestNewApplicationDoesNotAffectNamespace(t *testing.T) {
	ns := &Namespace{name: "test"}
	ns.addApplication(ApplicationSettings{Name: "existing", Host: "existing.localhost"})

	// NewApplication creates a standalone app without adding it to the namespace
	app := NewApplication(ns, ApplicationSettings{Name: "standalone", Host: "standalone.localhost"})

	assert.Nil(t, ns.Application("standalone"))
	assert.False(t, ns.HostInUse("standalone.localhost"))
	assert.Len(t, ns.Applications(), 1)
	assert.Equal(t, "standalone", app.Settings.Name)
}

func TestContainerAppName(t *testing.T) {
	ns := &Namespace{name: "once"}

	t.Run("standard app", func(t *testing.T) {
		assert.Equal(t, "campfire", ns.containerAppName("once-app-campfire-a1b2c3"))
	})

	t.Run("dotted unique name", func(t *testing.T) {
		assert.Equal(t, "campfire.a1b2c3", ns.containerAppName("once-app-campfire.a1b2c3-d4e5f6"))
	})

	t.Run("dashed app name", func(t *testing.T) {
		assert.Equal(t, "my-app", ns.containerAppName("once-app-my-app-abcdef"))
	})

	t.Run("wrong namespace", func(t *testing.T) {
		assert.Equal(t, "", ns.containerAppName("other-app-campfire-a1b2c3"))
	})

	t.Run("not a container name", func(t *testing.T) {
		assert.Equal(t, "", ns.containerAppName("something-else"))
	})

	t.Run("no ID suffix", func(t *testing.T) {
		assert.Equal(t, "", ns.containerAppName("once-app-campfire"))
	})
}

type mockRestoreClient struct {
	containers []container.Summary
	listErr    error
	startedAt  map[string]string
	inspectErr map[string]error
	inspected  []string
}

func (m *mockRestoreClient) ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.containers, nil
}

// A successful inspect always carries a populated base, as the daemon's does.
func (m *mockRestoreClient) ContainerInspect(ctx context.Context, containerID string) (container.InspectResponse, error) {
	m.inspected = append(m.inspected, containerID)
	if err := m.inspectErr[containerID]; err != nil {
		return container.InspectResponse{}, err
	}
	return container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{
			State: &container.State{StartedAt: m.startedAt[containerID]},
		},
	}, nil
}

func TestRestoreStateDecodesApplicationLabels(t *testing.T) {
	ns := newRestoreTestNamespace()
	client := &mockRestoreClient{
		containers: []container.Summary{
			appSummary("id-blog", "once-app-blog-a1b2c3", 100, "running", appSettings("blog")),
			appSummary("id-wiki", "once-app-wiki-d4e5f6", 200, "exited", appSettings("wiki")),
		},
	}

	require.NoError(t, ns.restoreState(context.Background(), client))

	require.Len(t, ns.Applications(), 2)

	blog := ns.Application("blog")
	require.NotNil(t, blog)
	assert.Equal(t, "blog.localhost", blog.Settings.Host)
	assert.Equal(t, "blog:latest", blog.Settings.Image)
	assert.True(t, blog.Running)

	wiki := ns.Application("wiki")
	require.NotNil(t, wiki)
	assert.False(t, wiki.Running)
}

func TestRestoreStateKeepsNewestContainerPerApp(t *testing.T) {
	older := appSettings("blog")
	older.Image = "blog:1"
	newer := appSettings("blog")
	newer.Image = "blog:2"

	restore := func(t *testing.T, containers ...container.Summary) *Application {
		ns := newRestoreTestNamespace()
		require.NoError(t, ns.restoreState(context.Background(), &mockRestoreClient{containers: containers}))
		require.Len(t, ns.Applications(), 1)
		return ns.Application("blog")
	}

	oldContainer := appSummary("id-old", "once-app-blog-a1b2c3", 100, "exited", older)
	newContainer := appSummary("id-new", "once-app-blog-d4e5f6", 200, "running", newer)

	t.Run("newest listed last", func(t *testing.T) {
		app := restore(t, oldContainer, newContainer)
		assert.Equal(t, "blog:2", app.Settings.Image)
		assert.True(t, app.Running)
	})

	t.Run("newest listed first", func(t *testing.T) {
		app := restore(t, newContainer, oldContainer)
		assert.Equal(t, "blog:2", app.Settings.Image)
		assert.True(t, app.Running)
	})
}

func TestRestoreStateDecodesProxyLabel(t *testing.T) {
	settings := ProxySettings{HTTPPort: 8080, HTTPSPort: 8443, MetricsPort: 9100}
	ns := newRestoreTestNamespace()
	client := &mockRestoreClient{
		containers: []container.Summary{
			labelledSummary("id-proxy", "once-proxy", 100, "running", settings.Marshal()),
		},
	}

	require.NoError(t, ns.restoreState(context.Background(), client))

	require.NotNil(t, ns.Proxy().Settings)
	assert.Equal(t, settings, *ns.Proxy().Settings)
}

func TestRestoreStateLeavesProxySettingsUnsetWithoutLabel(t *testing.T) {
	ns := newRestoreTestNamespace()
	client := &mockRestoreClient{
		containers: []container.Summary{
			labelledSummary("id-proxy", "once-proxy", 100, "running", ""),
			appSummary("id-blog", "once-app-blog-a1b2c3", 200, "exited", appSettings("blog")),
		},
	}

	require.NoError(t, ns.restoreState(context.Background(), client))

	assert.Nil(t, ns.Proxy().Settings)
	// The app proves the restore did walk the list past the unlabelled proxy.
	assert.Len(t, ns.Applications(), 1)
}

func TestRestoreStateFailsWhenListingContainersFails(t *testing.T) {
	ns := newRestoreTestNamespace()
	client := &mockRestoreClient{listErr: errors.New("daemon unreachable")}

	assert.ErrorContains(t, ns.restoreState(context.Background(), client), "daemon unreachable")
}

func TestRestoreStateCapturesRunningSince(t *testing.T) {
	startedAt := time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC)
	ns := newRestoreTestNamespace()
	client := &mockRestoreClient{
		containers: []container.Summary{
			appSummary("id-blog", "once-app-blog-a1b2c3", 100, "running", appSettings("blog")),
			appSummary("id-wiki", "once-app-wiki-d4e5f6", 200, "exited", appSettings("wiki")),
		},
		startedAt: map[string]string{"id-blog": startedAt.Format(time.RFC3339Nano)},
	}

	require.NoError(t, ns.restoreState(context.Background(), client))

	assert.Equal(t, startedAt, ns.Application("blog").RunningSince)
	assert.True(t, ns.Application("wiki").RunningSince.IsZero())
	// A stopped app has no start time to read, so it is never inspected.
	assert.Equal(t, []string{"id-blog"}, client.inspected)
}

// An inspect failure drops only the start time: the app is still restored, and
// still reported as running. Changing that belongs to the label module work.
func TestRestoreStateKeepsRunningAppWhenInspectFails(t *testing.T) {
	ns := newRestoreTestNamespace()
	client := &mockRestoreClient{
		containers: []container.Summary{
			appSummary("id-blog", "once-app-blog-a1b2c3", 100, "running", appSettings("blog")),
		},
		inspectErr: map[string]error{"id-blog": errors.New("no such container")},
	}

	require.NoError(t, ns.restoreState(context.Background(), client))

	blog := ns.Application("blog")
	require.NotNil(t, blog)
	assert.True(t, blog.Running)
	assert.True(t, blog.RunningSince.IsZero())
}

// A corrupt settings label aborts the whole restore, so apps decoded before it
// are dropped too. Changing that belongs to the label module work.
func TestRestoreStateFailsOnCorruptLabel(t *testing.T) {
	t.Run("app label", func(t *testing.T) {
		ns := newRestoreTestNamespace()
		client := &mockRestoreClient{
			containers: []container.Summary{
				appSummary("id-blog", "once-app-blog-a1b2c3", 100, "exited", appSettings("blog")),
				labelledSummary("id-wiki", "once-app-wiki-d4e5f6", 200, "exited", "{not json"),
			},
		}

		require.Error(t, ns.restoreState(context.Background(), client))
		assert.Empty(t, ns.Applications())
	})

	t.Run("proxy label", func(t *testing.T) {
		ns := newRestoreTestNamespace()
		client := &mockRestoreClient{
			containers: []container.Summary{
				labelledSummary("id-proxy", "once-proxy", 100, "running", "{not json"),
			},
		}

		require.Error(t, ns.restoreState(context.Background(), client))
		assert.Nil(t, ns.Proxy().Settings)
	})
}

func TestRestoreStateIgnoresContainersOutsideNamespace(t *testing.T) {
	ns := newRestoreTestNamespace()
	client := &mockRestoreClient{
		containers: []container.Summary{
			appSummary("id-blog", "once-app-blog-a1b2c3", 100, "exited", appSettings("blog")),
			labelledSummary("id-other-app", "other-app-wiki-d4e5f6", 200, "running", "{not json"),
			labelledSummary("id-other-proxy", "other-proxy", 200, "running", "{not json"),
			labelledSummary("id-sidecar", "once-tailscale", 200, "running", "{not json"),
			labelledSummary("id-admin", "once-admin", 200, "running", "{not json"),
			labelledSummary("id-unnamed", "", 200, "running", "{not json"),
		},
	}

	require.NoError(t, ns.restoreState(context.Background(), client))

	require.Len(t, ns.Applications(), 1)
	assert.Equal(t, "blog", ns.Applications()[0].Settings.Name)
	assert.Nil(t, ns.Proxy().Settings)
}

// Helpers

func newRestoreTestNamespace() *Namespace {
	ns := &Namespace{name: "once"}
	ns.proxy = NewProxy(ns)
	return ns
}

func appSummary(id, name string, created int64, state string, settings ApplicationSettings) container.Summary {
	return labelledSummary(id, name, created, state, settings.Marshal())
}

func labelledSummary(id, name string, created int64, state, label string) container.Summary {
	summary := container.Summary{
		ID:      id,
		Created: created,
		State:   state,
	}
	if name != "" {
		summary.Names = []string{"/" + name}
	}
	if label != "" {
		summary.Labels = map[string]string{labelKey: label}
	}
	return summary
}

func appSettings(name string) ApplicationSettings {
	settings := DefaultApplicationSettings()
	settings.Name = name
	settings.Host = name + ".localhost"
	settings.Image = name + ":latest"
	return settings
}
