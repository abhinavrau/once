package command

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
)

func TestRenderBackupStatusNotConfigured(t *testing.T) {
	out := renderBackupStatus(docker.BackupStatus{})
	assert.Equal(t, "Backups are not configured. Run `once backup enable` to set them up.\n", out)
}

// The happy path has to read as healthy: every fact present, nothing shouting.
func TestRenderBackupStatusHappyPath(t *testing.T) {
	at := time.Date(2026, 8, 14, 3, 4, 0, 0, time.Local)
	out := renderBackupStatus(docker.BackupStatus{
		Configured: true,
		Enabled:    true,
		LocalPath:  "/var/lib/once/backups",
		OffsiteURL: "s3:s3.amazonaws.com/bucket/once",
		Checks: map[string]docker.OperationResult{
			docker.RepoNameLocal:   {At: at},
			docker.RepoNameOffsite: {At: at.Add(2 * time.Minute)},
		},
		Apps: []docker.AppBackupStatus{{
			Name:       "vaultwarden",
			Included:   true,
			LastBackup: &docker.OperationResult{At: at},
			Offsite:    docker.OffsiteAge{Queried: true, Latest: time.Now().Add(-6 * time.Hour)},
		}},
	})

	assert.Contains(t, out, "Backups: enabled")
	assert.Contains(t, out, "Local repository:   /var/lib/once/backups")
	assert.Contains(t, out, "Offsite repository: s3:s3.amazonaws.com/bucket/once")
	assert.Contains(t, out, "vaultwarden  last backup ok 2026-08-14 03:04, offsite copy is 6 hours old")
	assert.Contains(t, out, "local    ok 2026-08-14 03:04")
	assert.Contains(t, out, "offsite  ok 2026-08-14 03:06")

	assert.NotContains(t, out, "Warning")
	assert.NotContains(t, out, "PROBLEM")
	assert.NotContains(t, out, "not enabled")
	for line := range strings.Lines(out) {
		assert.LessOrEqual(t, len(strings.TrimRight(line, "\n")), 80, "status lines stay readable in an 80-column terminal")
	}
}

// Configured-but-not-enabled has to carry the reason it never got enabled.
func TestRenderBackupStatusConfiguredButNotEnabled(t *testing.T) {
	at := time.Date(2026, 8, 13, 21, 11, 0, 0, time.Local)
	out := renderBackupStatus(docker.BackupStatus{
		Configured: true,
		LocalPath:  "/var/lib/once/backups",
		OffsiteURL: "s3:s3.amazonaws.com/bucket/once",
		LastProbe:  &docker.OperationResult{At: at, Error: "offsite repository: restic init failed"},
	})

	assert.Contains(t, out, "Backups: configured, not enabled")
	assert.Contains(t, out, "Last validation:    failed 2026-08-13 21:11\n")
	assert.Contains(t, out, "\n                      offsite repository: restic init failed\n")
	assert.Contains(t, out, "Run `once backup enable` to finish enabling backups.")
}

// The same-device warning is persistent and never a refusal, and what to do
// about it depends on whether an offsite copy already exists.
func TestRenderBackupStatusSameDeviceWarning(t *testing.T) {
	withOffsite := renderBackupStatus(docker.BackupStatus{
		Configured: true, Enabled: true, OnDataDevice: true,
		LocalPath: "/var/lib/once/backups", OffsiteURL: "/mnt/offsite",
	})
	assert.Contains(t, withOffsite, "Warning: the local repository is on the same disk as your application data.")
	assert.Contains(t, withOffsite, "the offsite copy is your only protection")

	withoutOffsite := renderBackupStatus(docker.BackupStatus{
		Configured: true, Enabled: true, OnDataDevice: true,
		LocalPath: "/var/lib/once/backups",
	})
	assert.Contains(t, withoutOffsite, "set an offsite repository")
}

// A damaged repo is a different emergency from a backup that did not run, so
// it gets its own word rather than sharing "failed".
func TestRenderBackupStatusCheckAlarm(t *testing.T) {
	at := time.Date(2026, 8, 14, 3, 4, 0, 0, time.Local)
	out := renderBackupStatus(docker.BackupStatus{
		Configured: true, Enabled: true, LocalPath: "/var/lib/once/backups",
		Checks: map[string]docker.OperationResult{
			docker.RepoNameLocal: {At: at, Error: "restic check found problems: 2 errors"},
		},
	})

	// The verdict on the row, the unbounded restic reason on its own line.
	assert.Contains(t, out, "  local    PROBLEM 2026-08-14 03:04\n")
	assert.Contains(t, out, "\n           restic check found problems: 2 errors\n")
	assert.Contains(t, out, "A repository that fails its check may not restore.")
}

// An unreachable offsite repo is unknown staleness - never "no offsite copy",
// which would tell the user with a full repo that they have nothing.
func TestRenderBackupStatusUnknownStaleness(t *testing.T) {
	out := renderBackupStatus(docker.BackupStatus{
		Configured: true, Enabled: true,
		LocalPath: "/var/lib/once/backups", OffsiteURL: "s3:s3.amazonaws.com/bucket/once",
		Apps: []docker.AppBackupStatus{{
			Name:       "vaultwarden",
			Included:   true,
			LastBackup: &docker.OperationResult{At: time.Date(2026, 8, 14, 3, 4, 0, 0, time.Local)},
			Offsite:    docker.OffsiteAge{Queried: true, Err: errors.New("restic snapshots failed: dial tcp: timeout")},
		}},
	})

	assert.Contains(t, out, "offsite copy age unknown")
	assert.Contains(t, out, "offsite: restic snapshots failed: dial tcp: timeout")
	assert.NotContains(t, out, "no offsite copy yet")
	assert.NotContains(t, out, "never backed up")
}

func TestRenderBackupStatusAppRows(t *testing.T) {
	at := time.Date(2026, 8, 14, 3, 4, 0, 0, time.Local)
	out := renderBackupStatus(docker.BackupStatus{
		Configured: true, Enabled: true, LocalPath: "/var/lib/once/backups",
		Apps: []docker.AppBackupStatus{
			{Name: "vaultwarden", Included: true, LastBackup: &docker.OperationResult{At: at, Error: "not enough free disk"}},
			{Name: "wb", Included: true},
			{Name: "writebook", Included: false},
		},
	})

	// The name column aligns on the longest name, and a failure's message goes
	// on its own line so the table stays a table.
	assert.Contains(t, out, "  vaultwarden  last backup failed 2026-08-14 03:04")
	assert.Contains(t, out, "\n               not enough free disk\n")
	assert.Contains(t, out, "  wb           never backed up")
	assert.Contains(t, out, "  writebook    not included in backups")
}

func TestDescribeOffsiteAge(t *testing.T) {
	assert.Equal(t, "offsite copy is 3 days old",
		describeOffsiteAge(docker.OffsiteAge{Queried: true, Latest: time.Now().Add(-73 * time.Hour)}))
	assert.Equal(t, "offsite copy is 6 hours old",
		describeOffsiteAge(docker.OffsiteAge{Queried: true, Latest: time.Now().Add(-6 * time.Hour)}))
	assert.Equal(t, "offsite copy is less than an hour old",
		describeOffsiteAge(docker.OffsiteAge{Queried: true, Latest: time.Now().Add(-time.Minute)}))
	assert.Equal(t, "no offsite copy yet", describeOffsiteAge(docker.OffsiteAge{Queried: true}))
	assert.Equal(t, "offsite copy age unknown",
		describeOffsiteAge(docker.OffsiteAge{Queried: true, Err: errors.New("unreachable")}))
	assert.Empty(t, describeOffsiteAge(docker.OffsiteAge{}))
}

// The verdict never carries the reason: restic messages are unbounded, and
// inlining one turns every aligned row into ragged wrap.
func TestDescribeOutcomeOmitsTheReason(t *testing.T) {
	at := time.Date(2026, 8, 14, 3, 4, 0, 0, time.Local)
	assert.Equal(t, "never backed up", describeLastBackup(nil))
	assert.Equal(t, "ok 2026-08-14 03:04", describeOutcome(docker.OperationResult{At: at}))

	failed := describeOutcome(docker.OperationResult{At: at, Error: "boom"})
	require.Equal(t, "failed 2026-08-14 03:04", failed)
	assert.NotContains(t, failed, "boom")
}
