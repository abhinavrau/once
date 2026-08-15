package command

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
)

func TestApplyChanges(t *testing.T) {
	existing := docker.ApplicationSettings{
		Name:       "myapp",
		Image:      "myimage:latest",
		Host:       "app.example.com",
		Exposure:   docker.ExposurePublic,
		DisableTLS: false,
		EnvVars:    map[string]string{"KEY": "value"},
		SMTP: docker.SMTPSettings{
			Server:   "smtp.example.com",
			Port:     "587",
			Username: "user",
			Password: "pass",
			From:     "noreply@example.com",
		},
		Resources: docker.ContainerResources{
			CPUs:     2,
			MemoryMB: 1024,
		},
		AutoUpdate: true,
		Backup: docker.BackupSettings{AutoBackup: true},
	}

	newCmd := func() (*cobra.Command, *settingsFlags) {
		cmd := &cobra.Command{}
		f := &settingsFlags{}
		f.register(cmd)
		return cmd, f
	}

	t.Run("no flags changed returns existing", func(t *testing.T) {
		cmd, f := newCmd()
		result, err := f.applyChanges(cmd, existing, existing.Image)
		require.NoError(t, err)
		assert.True(t, existing.Equal(result))
	})

	t.Run("single flag changed", func(t *testing.T) {
		cmd, f := newCmd()
		require.NoError(t, cmd.Flags().Set("memory", "2048"))

		result, err := f.applyChanges(cmd, existing, existing.Image)
		require.NoError(t, err)
		assert.Equal(t, 2048, result.Resources.MemoryMB)
		assert.Equal(t, existing.Resources.CPUs, result.Resources.CPUs)
		assert.Equal(t, existing.Host, result.Host)
		assert.Equal(t, existing.EnvVars, result.EnvVars)
	})

	t.Run("multiple flags changed", func(t *testing.T) {
		cmd, f := newCmd()
		require.NoError(t, cmd.Flags().Set("host", "new.example.com"))
		require.NoError(t, cmd.Flags().Set("cpus", "4"))
		require.NoError(t, cmd.Flags().Set("auto-backup", "false"))

		result, err := f.applyChanges(cmd, existing, existing.Image)
		require.NoError(t, err)
		assert.Equal(t, "new.example.com", result.Host)
		assert.Equal(t, 4, result.Resources.CPUs)
		assert.Equal(t, false, result.Backup.AutoBackup)
		// Unchanged fields preserved
		assert.Equal(t, existing.SMTP, result.SMTP)
		assert.Equal(t, existing.EnvVars, result.EnvVars)
		assert.Equal(t, existing.Resources.MemoryMB, result.Resources.MemoryMB)
	})

	t.Run("env replaces all vars", func(t *testing.T) {
		cmd, f := newCmd()
		require.NoError(t, cmd.Flags().Set("env", "NEW=val"))

		result, err := f.applyChanges(cmd, existing, existing.Image)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"NEW": "val"}, result.EnvVars)
	})

	t.Run("invalid env returns error", func(t *testing.T) {
		cmd, f := newCmd()
		require.NoError(t, cmd.Flags().Set("env", "INVALID"))

		_, err := f.applyChanges(cmd, existing, existing.Image)
		assert.ErrorContains(t, err, "must be in KEY=VALUE format")
	})

	t.Run("empty image is left for ApplySettings to reject", func(t *testing.T) {
		cmd, f := newCmd()

		result, err := f.applyChanges(cmd, existing, "")
		require.NoError(t, err)
		assert.ErrorIs(t, result.Validate(), docker.ErrImageRequired)
	})

	t.Run("enable auto-backup", func(t *testing.T) {
		noAutoBackup := existing
		noAutoBackup.Backup.AutoBackup = false

		cmd, f := newCmd()
		require.NoError(t, cmd.Flags().Set("auto-backup", "true"))

		result, err := f.applyChanges(cmd, noAutoBackup, noAutoBackup.Image)
		require.NoError(t, err)
		assert.True(t, result.Backup.AutoBackup)
	})

	t.Run("removed backup-path flag names its replacement", func(t *testing.T) {
		cmd, f := newCmd()
		require.NoError(t, cmd.Flags().Set("backup-path", "/backups"))

		err := f.checkRemoved(cmd)
		assert.ErrorContains(t, err, "once backup enable")
	})

	t.Run("profile flags update the App Profile", func(t *testing.T) {
		cmd, f := newCmd()
		require.NoError(t, cmd.Flags().Set("data-path", "/data"))
		require.NoError(t, cmd.Flags().Set("health-check-path", "/alive"))
		require.NoError(t, cmd.Flags().Set("url-env-var", "DOMAIN"))

		result, err := f.applyChanges(cmd, existing, existing.Image)
		require.NoError(t, err)
		assert.Equal(t, "/data", result.Profile.DataPath)
		assert.Equal(t, "/alive", result.Profile.HealthPath)
		assert.Equal(t, "DOMAIN", result.Profile.URLEnvVar)
	})
}
