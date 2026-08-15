package command

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
)

func configuredBackups() docker.BackupRepoSettings {
	return docker.BackupRepoSettings{
		Enabled:    true,
		LocalPath:  "/var/lib/once/backups",
		OffsiteURL: "s3:s3.example.com/bucket/once",
		OffsiteEnv: map[string]string{"AWS_ACCESS_KEY_ID": "stored-id"},
		Password:   "STOREDPASSWORD",
	}
}

// A configured install reads its own local repo, and never quietly reaches for
// the offsite copy instead: which of the two copies was trusted is exactly
// what a restore must not hide.
func TestResolveRepoDefaultsToTheLocalRepo(t *testing.T) {
	repo, err := (&restoreCommand{}).resolveRepo(configuredBackups())
	require.NoError(t, err)

	assert.Equal(t, "/var/lib/once/backups", repo.URL)
	assert.Equal(t, "STOREDPASSWORD", repo.Password, "a repo this install owns comes with its password")
	assert.Equal(t, "/var/lib/once/backups.staging", repo.StagingDir)
}

func TestResolveRepoFlagOverridesTheConfiguredRepo(t *testing.T) {
	repo, err := (&restoreCommand{repo: "s3:s3.example.com/bucket/once"}).resolveRepo(configuredBackups())
	require.NoError(t, err)

	assert.Equal(t, "s3:s3.example.com/bucket/once", repo.URL)
	assert.Equal(t, "STOREDPASSWORD", repo.Password)
	assert.Equal(t, map[string]string{"AWS_ACCESS_KEY_ID": "stored-id"}, repo.Env)
}

// The bare-metal case: nothing is configured, so the repo must be named and
// the Staging Directory falls back to the install's default path.
func TestResolveRepoOnAFreshInstall(t *testing.T) {
	t.Setenv("RESTIC_PASSWORD", "FROMENVIRONMENT")

	repo, err := (&restoreCommand{repo: "/mnt/usb/once"}).resolveRepo(docker.BackupRepoSettings{})
	require.NoError(t, err)

	assert.Equal(t, "/mnt/usb/once", repo.URL)
	assert.Equal(t, "FROMENVIRONMENT", repo.Password)
	assert.Equal(t, "/var/lib/once/backups.staging", repo.StagingDir)
}

func TestResolveRepoWithoutAnyRepository(t *testing.T) {
	_, err := (&restoreCommand{}).resolveRepo(docker.BackupRepoSettings{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--repo")
}

// RESTIC_PASSWORD is machine-fed and taken verbatim; a password Once did not
// store and the environment does not hold has to be typed, which the tests
// cannot do - and neither can a scripted run, which gets told how instead of
// hanging on a prompt it has no terminal for.
func TestResolveRepoPassword(t *testing.T) {
	cfg := configuredBackups()

	password, err := resolveRepoPassword("s3:s3.example.com/bucket/once", cfg)
	require.NoError(t, err)
	assert.Equal(t, "STOREDPASSWORD", password)

	t.Setenv("RESTIC_PASSWORD", "FROMENVIRONMENT")
	password, err = resolveRepoPassword("/somewhere/else", cfg)
	require.NoError(t, err)
	assert.Equal(t, "FROMENVIRONMENT", password)

	t.Setenv("RESTIC_PASSWORD", "")
	_, err = resolveRepoPassword("/somewhere/else", cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RESTIC_PASSWORD")
}

// Exporting a variable is a deliberate act, so it wins over what was stored;
// with neither, the repo carries no environment at all.
func TestRestoreRepoEnv(t *testing.T) {
	cfg := configuredBackups()

	assert.Equal(t, map[string]string{"AWS_ACCESS_KEY_ID": "stored-id"},
		restoreRepoEnv(cfg.OffsiteURL, cfg))
	assert.Nil(t, restoreRepoEnv("/mnt/usb/once", cfg),
		"the stored credentials belong to the offsite repo, not to every repo")

	t.Setenv("AWS_ACCESS_KEY_ID", "live-id")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "live-secret")
	assert.Equal(t, map[string]string{"AWS_ACCESS_KEY_ID": "live-id", "AWS_SECRET_ACCESS_KEY": "live-secret"},
		restoreRepoEnv(cfg.OffsiteURL, cfg))
}

func TestFormatRepoListing(t *testing.T) {
	apps := []docker.RepoApp{
		{Name: "campfire", Snapshots: 1, Newest: time.Date(2026, 8, 9, 3, 0, 0, 0, time.Local), Image: "ghcr.io/basecamp/once-campfire:main"},
		{Name: "vaultwarden", Snapshots: 3, Newest: time.Date(2026, 8, 10, 3, 0, 0, 0, time.Local), Image: "vaultwarden/server:1.32"},
	}

	out := formatRepoListing("/mnt/usb/once", apps)

	assert.Contains(t, out, "campfire")
	assert.Contains(t, out, "1 snapshot ")
	assert.Contains(t, out, "3 snapshots")
	assert.Contains(t, out, "newest 2026-08-10 03:00")
	assert.Contains(t, out, "vaultwarden/server:1.32")
	assert.Contains(t, out, "once restore <app> --repo /mnt/usb/once")

	assert.Contains(t, formatRepoListing("/mnt/usb/once", nil), "no application backups")
}

func TestFormatSnapshotHistory(t *testing.T) {
	out := formatSnapshotHistory("vaultwarden", "/mnt/usb/once", docker.RepoSnapshots{
		{ID: "a1b2c3d4", Time: time.Date(2026, 8, 9, 3, 0, 0, 0, time.Local), Image: "vaultwarden/server:1.32"},
		{ID: "e5f6a7b8", Time: time.Date(2026, 8, 10, 3, 0, 0, 0, time.Local), Image: "vaultwarden/server:1.33"},
	})

	assert.Contains(t, out, "a1b2c3d4 2026-08-09 03:00  vaultwarden/server:1.32")
	assert.Contains(t, out, "e5f6a7b8 2026-08-10 03:00  vaultwarden/server:1.33")
	assert.Contains(t, out, "--snapshot <id>")
}

// The pick names everything needed to tell a wrong choice from a right one -
// including the host that wrote it, which on a bare-metal recovery is the
// machine that is gone.
func TestFormatRestorePick(t *testing.T) {
	out := formatRestorePick("/mnt/usb/once", docker.RepoSnapshot{
		ID:    "a1b2c3d4",
		Time:  time.Date(2026, 8, 10, 3, 0, 0, 0, time.Local),
		App:   "vaultwarden",
		Image: "vaultwarden/server:1.32",
		Host:  "once",
	})

	assert.Contains(t, out, "Restoring vaultwarden from /mnt/usb/once")
	assert.Contains(t, out, "snapshot a1b2c3d4 taken 2026-08-10 03:00 on host once")
	assert.Contains(t, out, "image vaultwarden/server:1.32")
	assert.NotContains(t, out, "[y/n]", "a restore is additive: there is nothing to confirm away")
}

func TestPickSnapshot(t *testing.T) {
	history := docker.RepoSnapshots{
		{ID: "a1b2c3d4", App: "vaultwarden", Time: time.Date(2026, 8, 9, 3, 0, 0, 0, time.UTC)},
		{ID: "e5f6a7b8", App: "vaultwarden", Time: time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC)},
	}

	pick, err := (&restoreCommand{}).pickSnapshot(history, "vaultwarden")
	require.NoError(t, err)
	assert.Equal(t, "e5f6a7b8", pick.ID, "the newest snapshot is the default")

	pick, err = (&restoreCommand{snapshot: "a1b2c3d4"}).pickSnapshot(history, "vaultwarden")
	require.NoError(t, err)
	assert.Equal(t, "a1b2c3d4", pick.ID)

	_, err = (&restoreCommand{snapshot: "deadbeef"}).pickSnapshot(history, "vaultwarden")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--list")
}
