package command

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/catalog"
	"github.com/basecamp/once/internal/docker"
)

func TestParseEnvVars(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		f := &settingsFlags{}
		result, err := f.parseEnvVars()
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("valid pairs", func(t *testing.T) {
		f := &settingsFlags{env: []string{"FOO=bar", "BAZ=qux"}}
		result, err := f.parseEnvVars()
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"FOO": "bar", "BAZ": "qux"}, result)
	})

	t.Run("value containing equals", func(t *testing.T) {
		f := &settingsFlags{env: []string{"DSN=postgres://host?opt=val"}}
		result, err := f.parseEnvVars()
		require.NoError(t, err)
		assert.Equal(t, "postgres://host?opt=val", result["DSN"])
	})

	t.Run("missing equals", func(t *testing.T) {
		f := &settingsFlags{env: []string{"INVALID"}}
		_, err := f.parseEnvVars()
		assert.ErrorContains(t, err, "must be in KEY=VALUE format")
	})

	t.Run("empty key", func(t *testing.T) {
		f := &settingsFlags{env: []string{"=value"}}
		_, err := f.parseEnvVars()
		assert.ErrorContains(t, err, "key must not be empty")
	})

	t.Run("empty value is valid", func(t *testing.T) {
		f := &settingsFlags{env: []string{"KEY="}}
		result, err := f.parseEnvVars()
		require.NoError(t, err)
		assert.Equal(t, "", result["KEY"])
	})

	t.Run("duplicate keys last wins", func(t *testing.T) {
		f := &settingsFlags{env: []string{"KEY=first", "KEY=second"}}
		result, err := f.parseEnvVars()
		require.NoError(t, err)
		assert.Equal(t, "second", result["KEY"])
	})
}

func TestBuildSettingsStoresExposure(t *testing.T) {
	f := &settingsFlags{}
	s, err := f.buildSettings(catalog.App{ImageRef: "image:latest"}, "app.localhost", docker.ExposureLocal)
	require.NoError(t, err)
	assert.Equal(t, docker.ExposureLocal, s.Exposure)

	s, err = f.buildSettings(catalog.App{ImageRef: "image:latest"}, "app.example.com", docker.ExposurePublic)
	require.NoError(t, err)
	assert.Equal(t, docker.ExposurePublic, s.Exposure)
}

func TestApplyChangesExposure(t *testing.T) {
	apply := func(existing docker.ApplicationSettings, args ...string) (docker.ApplicationSettings, error) {
		cmd := &cobra.Command{}
		f := &settingsFlags{}
		f.register(cmd)
		require.NoError(t, cmd.Flags().Parse(args))
		return f.applyChanges(cmd, existing, existing.Image)
	}

	local := docker.ApplicationSettings{Name: "app", Image: "image:1", Host: "app.localhost", Exposure: docker.ExposureLocal}

	// Unset flag leaves the stored exposure untouched.
	s, err := apply(local)
	require.NoError(t, err)
	assert.Equal(t, docker.ExposureLocal, s.Exposure)

	// Promoting to public requires a real FQDN.
	_, err = apply(local, "--expose", "public")
	assert.ErrorIs(t, err, docker.ErrPublicHostRequired)

	s, err = apply(local, "--expose", "public", "--host", "app.example.com")
	require.NoError(t, err)
	assert.Equal(t, docker.ExposurePublic, s.Exposure)
	assert.True(t, s.TLSEnabled())

	// A bare host under local exposure gets the .localhost suffix.
	s, err = apply(local, "--host", "writebook")
	require.NoError(t, err)
	assert.Equal(t, "writebook.localhost", s.Host)

	// Switching exposure without --host re-derives the host from the app
	// name: the old exposure's host shape doesn't carry over.
	s, err = apply(local, "--expose", "tailnet")
	require.NoError(t, err)
	assert.Equal(t, docker.ExposureTailnet, s.Exposure)
	assert.Equal(t, "image", s.Host)

	s, err = apply(local, "--expose", "tailnet", "--host", "books")
	require.NoError(t, err)
	assert.Equal(t, "books", s.Host)

	_, err = apply(local, "--expose", "tailnet", "--host", "My_App")
	assert.ErrorContains(t, err, "not a valid DNS label")

	tailnet := docker.ApplicationSettings{Name: "app", Image: "image:1", Host: "books", Exposure: docker.ExposureTailnet}
	s, err = apply(tailnet, "--expose", "local")
	require.NoError(t, err)
	assert.Equal(t, docker.ExposureLocal, s.Exposure)
	assert.Equal(t, "image.localhost", s.Host)

	// Promoting a tailnet app to public still demands an explicit FQDN.
	_, err = apply(tailnet, "--expose", "public")
	assert.ErrorIs(t, err, docker.ErrPublicHostRequired)
}

func TestRemovedTailscaleFlagFailsWithReplacement(t *testing.T) {
	for _, arg := range []string{"--tailscale", "--tailscale=false"} {
		cmd := &cobra.Command{}
		f := &settingsFlags{}
		f.register(cmd)
		require.NoError(t, cmd.Flags().Parse([]string{arg}))
		err := f.checkRemoved(cmd)
		assert.ErrorContains(t, err, "--expose tailnet")
	}

	cmd := &cobra.Command{}
	f := &settingsFlags{}
	f.register(cmd)
	require.NoError(t, cmd.Flags().Parse(nil))
	assert.NoError(t, f.checkRemoved(cmd))
}

// Anchors the flagless CLI deploy path to DefaultApplicationSettings. Fails
// when a flag gains a non-zero default that isn't written there, keeping CLI
// deploys and TUI installs (anchored by TestInstallSettingsStartFromDefaults
// in internal/ui) from drifting apart.
func TestFlaglessDeploySettingsAreTheDefaults(t *testing.T) {
	cmd := &cobra.Command{}
	f := &settingsFlags{}
	f.register(cmd)
	require.NoError(t, cmd.Flags().Parse(nil))

	exposure, err := docker.ParseExposure(f.expose)
	require.NoError(t, err)

	s, err := f.buildSettings(catalog.App{ImageRef: "image:latest"}, "app.localhost", exposure)
	require.NoError(t, err)

	want := docker.DefaultApplicationSettings()
	want.Image = "image:latest"
	want.Host = "app.localhost"
	want.Exposure = exposure
	assert.Equal(t, want, s)
}
