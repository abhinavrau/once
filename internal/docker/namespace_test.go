package docker

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"
	"time"

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

	// With no host socket, --mode host must fail on the pre-flight before the
	// credential probe (the error is the socket one, not a credentials one) and
	// before any Docker work (a nil client would panic past that point).
	_, err = ns.EnableTailscale(context.Background(),
		TailscaleSettings{ClientID: "id", ClientSecret: "secret", Tag: "tag:once", Mode: TailscaleModeHost}, nil)

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
		port, err := ns.allocateFunnelPort(app)
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
	port, err := ns.allocateFunnelPort(fizzy)
	require.NoError(t, err)
	assert.Equal(t, 8443, port)
	_, err = ns.allocateFunnelPort(newApp("fourth"))
	assert.ErrorIs(t, err, ErrFunnelPortsExhausted)

	// A pre-allocation label (funnel active, no port recorded) holds 443.
	ns2 := &Namespace{name: "once"}
	legacy := NewApplication(ns2, ApplicationSettings{Name: "legacy", Host: "legacy", Exposure: ExposureTailnet})
	expires := time.Now().Add(time.Hour)
	legacy.Settings.FunnelExpiresAt = &expires
	next := NewApplication(ns2, ApplicationSettings{Name: "next", Host: "next", Exposure: ExposureTailnet})
	ns2.applications = append(ns2.applications, legacy, next)
	port, err = ns2.allocateFunnelPort(next)
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
