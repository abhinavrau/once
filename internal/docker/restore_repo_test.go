package docker

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Verbatim `restic snapshots --json` output from restic 0.19.1, including the
// per-snapshot summary that carries the archive size: a restore reads the app
// name, the image, the original host, and the size it must make room for out
// of this and nothing else.
const resticSnapshotsJSON = `[
  {"time":"2026-08-09T03:00:01.1Z","tree":"aa","paths":["/staging/vaultwarden.tar"],"hostname":"once","username":"root",
   "tags":["app:vaultwarden","image:vaultwarden/server:1.32"],
   "summary":{"total_files_processed":1,"total_bytes_processed":4096},
   "id":"1111111111111111","short_id":"11111111"},
  {"time":"2026-08-10T03:00:02.2Z","tree":"bb","paths":["/staging/vaultwarden.tar"],"hostname":"once","username":"root",
   "tags":["app:vaultwarden","image:vaultwarden/server:1.33"],
   "summary":{"total_files_processed":1,"total_bytes_processed":8192},
   "id":"2222222222222222","short_id":"22222222"},
  {"time":"2026-08-10T04:00:00Z","tree":"cc","paths":["/staging/campfire.tar"],"hostname":"other-host","username":"root",
   "tags":["app:campfire","image:ghcr.io/basecamp/once-campfire:main"],
   "summary":{"total_files_processed":1,"total_bytes_processed":16384},
   "id":"3333333333333333","short_id":"33333333"},
  {"time":"2026-08-11T05:00:00Z","tree":"dd","paths":["/staging/.once-probe"],"hostname":"once","username":"root",
   "tags":[],"id":"4444444444444444","short_id":"44444444"}
]`

func TestParseRepoSnapshots(t *testing.T) {
	snapshots, err := parseRepoSnapshots(resticSnapshotsJSON)
	require.NoError(t, err)

	require.Len(t, snapshots, 3, "a snapshot with no app tag - a probe leftover - is not something Once can restore")
	assert.Equal(t, RepoSnapshot{
		ID:    "11111111",
		Time:  time.Date(2026, 8, 9, 3, 0, 1, 100000000, time.UTC),
		App:   "vaultwarden",
		Image: "vaultwarden/server:1.32",
		Host:  "once",
		Path:  "/staging/vaultwarden.tar",
		Bytes: 4096,
	}, snapshots[0])
	assert.Equal(t, "other-host", snapshots[2].Host, "the original host travels: bare-metal recovery is reading another machine's snapshots")

	for i := 1; i < len(snapshots); i++ {
		assert.False(t, snapshots[i].Time.Before(snapshots[i-1].Time), "snapshots come back oldest first")
	}
}

// A snapshot written by a restic older than 0.17 carries no summary, so the
// size is unknown - which the free-disk check reads as "no baseline" and
// skips, rather than refusing the restore.
func TestParseRepoSnapshotsWithoutSummary(t *testing.T) {
	snapshots, err := parseRepoSnapshots(`[{"time":"2026-08-09T03:00:00Z","paths":["/staging/vaultwarden.tar"],
	  "hostname":"once","tags":["app:vaultwarden"],"id":"aaaa","short_id":"aaaa"}]`)
	require.NoError(t, err)

	require.Len(t, snapshots, 1)
	assert.Zero(t, snapshots[0].Bytes)
	assert.Empty(t, snapshots[0].Image)
	assert.NoError(t, backupDiskCheck(1, snapshots[0].Bytes))
}

func TestRepoSnapshotsApps(t *testing.T) {
	snapshots, err := parseRepoSnapshots(resticSnapshotsJSON)
	require.NoError(t, err)

	assert.Equal(t, []RepoApp{
		{Name: "campfire", Snapshots: 1, Newest: time.Date(2026, 8, 10, 4, 0, 0, 0, time.UTC), Image: "ghcr.io/basecamp/once-campfire:main"},
		{Name: "vaultwarden", Snapshots: 2, Newest: time.Date(2026, 8, 10, 3, 0, 2, 200000000, time.UTC), Image: "vaultwarden/server:1.33"},
	}, snapshots.Apps(), "one line per app, ordered by name, with the image from its newest snapshot")
}

func TestRepoSnapshotsSelection(t *testing.T) {
	snapshots, err := parseRepoSnapshots(resticSnapshotsJSON)
	require.NoError(t, err)

	latest, ok := snapshots.Latest("vaultwarden")
	require.True(t, ok)
	assert.Equal(t, "22222222", latest.ID, "the newest is the default pick")

	older, ok := snapshots.Find("vaultwarden", "11111111")
	require.True(t, ok)
	assert.Equal(t, "vaultwarden/server:1.32", older.Image)

	_, ok = snapshots.Find("vaultwarden", "33333333")
	assert.False(t, ok, "a snapshot id is only valid for the app it belongs to")

	_, ok = snapshots.Latest("nothing-here")
	assert.False(t, ok)
}

// The restore reads the snapshot's Staging Directory subfolder into the
// Staging Directory: exactly the file the Backup Run wrote, at the path every
// snapshot records.
func TestResticRestoreArgs(t *testing.T) {
	assert.Equal(t, []string{"restore", "a1b2c3d4:/staging", "--target", "/staging"}, resticRestoreArgs("a1b2c3d4"))
}

// The listing is a pure question: no lock is taken against a repo that may be
// shared, and neither host nor tag scopes it - the install that wrote these
// snapshots is the one that is gone.
func TestResticRepoSnapshotsArgs(t *testing.T) {
	assert.Equal(t, []string{"snapshots", "--json", "--no-lock"}, resticRepoSnapshotsArgs())
}

func TestRestoreResticRepoFromURL(t *testing.T) {
	repo := restoreResticRepo(RestoreRepo{
		URL:        "s3:s3.example.com/bucket/once",
		Password:   "secret",
		Env:        map[string]string{"AWS_ACCESS_KEY_ID": "id"},
		StagingDir: "/var/lib/once/backups.staging",
	})

	assert.Equal(t, "s3:s3.example.com/bucket/once", repo.repository)
	assert.Equal(t, "s3:s3.example.com/bucket/once", repo.displayName)
	assert.Equal(t, map[string]string{"AWS_ACCESS_KEY_ID": "id"}, repo.env)
	assert.Empty(t, repo.localRepoDir, "a URL repo is reached over the network, never mounted")
	assert.True(t, repo.stagingWritable, "restore is the one command that writes the Staging Directory from inside")
}

// A repository that is a filesystem path has to be bind-mounted: left
// unmounted, restic would look for it inside the container and report an empty
// repo instead of a missing one.
func TestRestoreResticRepoFromPath(t *testing.T) {
	repo := restoreResticRepo(RestoreRepo{URL: "/mnt/usb/once", Password: "secret", StagingDir: "/tmp/staging"})

	assert.Equal(t, "/repo", repo.repository)
	assert.Equal(t, "/mnt/usb/once", repo.displayName, "logs and errors name the repo the user gave")
	assert.Equal(t, "/mnt/usb/once", repo.localRepoDir)
}

// After a bare-metal recovery the remote is the repo that exists, so the fresh
// local one copies its chunker parameters - the mirror image of the offsite
// init, and what keeps a later `restic copy` a one-shot backfill.
func TestLocalResticRepoFromOffsite(t *testing.T) {
	cfg := BackupRepoSettings{
		LocalPath:  "/var/lib/once/backups",
		OffsiteURL: "s3:s3.example.com/bucket/once",
		OffsiteEnv: map[string]string{"AWS_ACCESS_KEY_ID": "id"},
		Password:   "secret",
	}

	repo := localResticRepoFrom(cfg)

	assert.Equal(t, "/repo", repo.repository)
	assert.Equal(t, "/var/lib/once/backups", repo.localRepoDir)
	assert.Equal(t, []string{"init", "--copy-chunker-params"}, repo.initArgs)
	assert.Equal(t, map[string]string{
		"AWS_ACCESS_KEY_ID":      "id",
		"RESTIC_FROM_REPOSITORY": "s3:s3.example.com/bucket/once",
		"RESTIC_FROM_PASSWORD":   "secret",
	}, repo.env)
}

// A filesystem-path offsite repo is mounted for the local init too, and named
// by its in-container path - the same substitution the offsite leg makes.
func TestLocalResticRepoFromOffsitePath(t *testing.T) {
	repo := localResticRepoFrom(BackupRepoSettings{
		LocalPath:  "/var/lib/once/backups",
		OffsiteURL: "/mnt/usb/once",
		Password:   "secret",
	})

	assert.Equal(t, "/mnt/usb/once", repo.offsiteRepoDir)
	assert.Equal(t, "/offsite", repo.env["RESTIC_FROM_REPOSITORY"])
}

// A restore onto a fresh install has no configured repo to hang the Staging
// Directory off, and ".staging" would land wherever the user ran the command.
func TestStagingDirOrDefault(t *testing.T) {
	assert.Equal(t, "/var/lib/once/backups.staging", BackupRepoSettings{}.StagingDirOrDefault())
	assert.Equal(t, "/mnt/disk2/repo.staging", BackupRepoSettings{LocalPath: "/mnt/disk2/repo"}.StagingDirOrDefault())
}
