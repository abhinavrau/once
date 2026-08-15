package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
)

func TestBackupsFormSubmitCollectsSettings(t *testing.T) {
	form := newBackupsForm(nil, docker.BackupRepoSettings{})

	view := form.View()
	assert.Contains(t, view, "Local repository path")
	assert.Contains(t, view, docker.DefaultBackupLocalPath, "the local path prefills the default")

	backupsFormFill(t, &form, "offsite-url", "s3:s3.example.com/bucket/once")
	backupsFormFill(t, &form, "cred-key-0", "AWS_ACCESS_KEY_ID")
	backupsFormFill(t, &form, "cred-value-0", "the-key-id")

	msg := backupsFormSubmit(t, &form)
	require.NotNil(t, msg)
	assert.Equal(t, docker.DefaultBackupLocalPath, msg.settings.LocalPath)
	assert.Equal(t, "s3:s3.example.com/bucket/once", msg.settings.OffsiteURL)
	assert.Equal(t, map[string]string{"AWS_ACCESS_KEY_ID": "the-key-id"}, msg.settings.OffsiteEnv)
	assert.Empty(t, msg.settings.Password, "the password is ConfigureBackups' to mint or reuse, never the form's")
	assert.False(t, msg.settings.Enabled, "Enabled is the probe's and the gate's to grant")
}

func TestBackupsFormReusesStoredPassword(t *testing.T) {
	stored := docker.BackupRepoSettings{
		LocalPath: "/mnt/backups",
		Password:  "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567",
	}
	form := newBackupsForm(nil, stored)

	msg := backupsFormSubmit(t, &form)
	require.NotNil(t, msg)
	assert.Equal(t, stored.Password, msg.settings.Password, "an existing password is reused, never regenerated")
	assert.Equal(t, "/mnt/backups", msg.settings.LocalPath)
}

func TestBackupsFormRejectsRelativeLocalPath(t *testing.T) {
	form := newBackupsForm(nil, docker.BackupRepoSettings{LocalPath: "relative/path"})

	msg := backupsFormSubmit(t, &form)
	assert.Nil(t, msg)
	assert.True(t, form.form.HasError())
}

// Typing into the last credential-name row grows a fresh empty pair, the
// environment form's pattern.
func TestBackupsFormGrowsCredentialRows(t *testing.T) {
	form := newBackupsForm(nil, docker.BackupRepoSettings{})
	require.Equal(t, 4, form.form.ItemCount(), "local path, offsite URL, one empty pair")

	backupsFormFill(t, &form, "cred-key-0", "B2_ACCOUNT_ID")
	assert.Equal(t, 6, form.form.ItemCount(), "a filled name row grows a fresh empty pair")
}

// Helpers

func backupsFormUpdate(t *testing.T, form *BackupsForm, msg tea.Msg) tea.Cmd {
	t.Helper()
	c, cmd := form.Update(msg)
	updated, ok := c.(BackupsForm)
	require.True(t, ok)
	*form = updated
	return cmd
}

func backupsFormFill(t *testing.T, form *BackupsForm, key, text string) {
	t.Helper()
	backupsFormUpdate(t, form, MouseEvent{Target: "field:" + key, IsClick: true})
	for _, r := range text {
		backupsFormUpdate(t, form, keyPressMsg(string(r)))
	}
}

// backupsFormSubmit clicks submit and returns the enable message, or nil when
// the form rejected the submit.
func backupsFormSubmit(t *testing.T, form *BackupsForm) *backupsEnableSubmitMsg {
	t.Helper()
	cmd := backupsFormUpdate(t, form, MouseEvent{Target: "submit", IsClick: true})
	for _, msg := range collectMsgs(cmd) {
		if enable, ok := msg.(backupsEnableSubmitMsg); ok {
			return &enable
		}
	}
	return nil
}
