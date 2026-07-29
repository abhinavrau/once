package docker

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The preconditions must run, in order, before any Docker work: a nil client
// would panic in Setup/Deploy if a check were reordered below it.
func TestApplySettingsPreconditionsRunBeforeDockerWork(t *testing.T) {
	ns := &Namespace{name: "test"}
	app := ns.addApplication(ApplicationSettings{Name: "app1", Host: "app1.localhost", Image: "img:latest"})
	ns.addApplication(ApplicationSettings{Name: "app2", Host: "app2.localhost", Image: "img:latest"})

	t.Run("invalid configuration is rejected first", func(t *testing.T) {
		invalid := app.Settings
		invalid.Image = ""
		_, err := ns.ApplySettings(context.Background(), app, invalid, nil)
		assert.ErrorIs(t, err, ErrImageRequired)
		assert.Equal(t, "img:latest", app.Settings.Image)
	})

	t.Run("an unchanged configuration short-circuits without rolling", func(t *testing.T) {
		warnings, err := ns.ApplySettings(context.Background(), app, app.Settings, nil)
		require.NoError(t, err)
		assert.Empty(t, warnings)
	})

	t.Run("a host used by another application is rejected", func(t *testing.T) {
		taken := app.Settings
		taken.Host = "app2.localhost"
		_, err := ns.ApplySettings(context.Background(), app, taken, nil)
		assert.ErrorIs(t, err, ErrHostnameInUse)
		assert.Equal(t, "app1.localhost", app.Settings.Host)
	})
}

func TestApplySettingsRestoresConfigurationWhenRollFails(t *testing.T) {
	ns := &Namespace{name: "test"}
	app := ns.addApplication(ApplicationSettings{Name: "app1", Host: "app1.localhost", Image: "img:latest"})

	// An empty Host makes Deploy fail before any Docker work (a nil client
	// would panic otherwise), standing in for any failed roll.
	broken := app.Settings
	broken.Host = ""
	err := rollConfiguration(context.Background(), app, broken, nil)

	require.ErrorIs(t, err, ErrDeployFailed)
	assert.ErrorIs(t, err, ErrHostRequired)
	assert.Equal(t, "app1.localhost", app.Settings.Host)
}
