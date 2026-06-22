package command

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

func TestBuildSettingsImageRequired(t *testing.T) {
	f := &settingsFlags{}
	_, err := f.buildSettings("", "app.example.com")
	assert.ErrorIs(t, err, docker.ErrImageRequired)
}

func TestBuildSettingsTailscaleExposure(t *testing.T) {
	exposed := &settingsFlags{tailscale: true}
	s, err := exposed.buildSettings("image:latest", "app.example.com")
	require.NoError(t, err)
	assert.True(t, s.TailscaleExposed())

	hidden := &settingsFlags{tailscale: false}
	s, err = hidden.buildSettings("image:latest", "app.example.com")
	require.NoError(t, err)
	assert.False(t, s.TailscaleExposed())
}

func TestApplyChangesTailscaleOnlyWhenFlagSet(t *testing.T) {
	cmd := &cobra.Command{}
	f := &settingsFlags{}
	f.register(cmd)
	require.NoError(t, cmd.Flags().Parse([]string{"--tailscale=false"}))

	existing := docker.ApplicationSettings{Name: "app", Image: "image:1"}
	s, err := f.applyChanges(cmd, existing, "image:1")
	require.NoError(t, err)
	assert.False(t, s.TailscaleExposed())

	// Unset flag leaves the existing exposure untouched.
	cmd2 := &cobra.Command{}
	f2 := &settingsFlags{}
	f2.register(cmd2)
	require.NoError(t, cmd2.Flags().Parse(nil))
	hidden := docker.ApplicationSettings{Name: "app", Image: "image:1", TailscaleExcluded: true}
	s, err = f2.applyChanges(cmd2, hidden, "image:1")
	require.NoError(t, err)
	assert.False(t, s.TailscaleExposed())
}

func TestBuildSettingsAutoBackupRequiresPath(t *testing.T) {
	t.Run("auto-backup without path", func(t *testing.T) {
		f := &settingsFlags{autoBackup: true}
		_, err := f.buildSettings("image:latest", "app.example.com")
		assert.ErrorIs(t, err, docker.ErrAutoBackupWithoutPath)
	})

	t.Run("auto-backup with path", func(t *testing.T) {
		f := &settingsFlags{autoBackup: true, backupPath: "/backups"}
		_, err := f.buildSettings("image:latest", "app.example.com")
		require.NoError(t, err)
	})
}
