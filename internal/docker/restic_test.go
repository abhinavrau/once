package docker

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Retention groups snapshots by --group-by host,paths. A --host or staging
// path that varied with time, container, or run would mint a new group per
// run, and retention would silently never delete anything - every command
// reporting success. Hence: both are constants of the namespace and app name
// alone.
func TestResticBackupArgsAreConstant(t *testing.T) {
	first := resticBackupArgs("once", "vaultwarden")
	second := resticBackupArgs("once", "vaultwarden")

	assert.Equal(t, first, second)
	assert.Equal(t, []string{
		"backup", "--json",
		"--host", "once",
		"--tag", "app:vaultwarden",
		"/staging/vaultwarden.tar",
	}, first)
}

// The probe backs up the same fixed path with the same --host every time, so
// repeated failed probes land in a single host,paths retention group instead
// of minting one group per attempt.
func TestResticProbeBackupArgsAreConstant(t *testing.T) {
	args := resticProbeBackupArgs("once")

	assert.Equal(t, args, resticProbeBackupArgs("once"))
	assert.Equal(t, []string{
		"backup", "--json",
		"--host", "once",
		"/staging/.once-probe",
	}, args)
}

// The offsite repo is addressed by URL with the backend credentials splatted
// into the environment, and carries the local repo mounted at /repo so init
// can copy its chunker params - which keeps a later `copy` a one-shot
// backfill instead of a full re-chunk.
func TestOffsiteResticRepo(t *testing.T) {
	cfg := BackupRepoSettings{
		LocalPath:  "/var/lib/once/backups",
		OffsiteURL: "s3:s3.example.com/bucket/once",
		OffsiteEnv: map[string]string{"AWS_ACCESS_KEY_ID": "id", "AWS_SECRET_ACCESS_KEY": "key"},
		Password:   "secret",
	}

	repo := offsiteResticRepo(cfg)

	assert.Equal(t, "s3:s3.example.com/bucket/once", repo.repository)
	assert.Equal(t, "secret", repo.password)
	assert.Equal(t, "/var/lib/once/backups", repo.hostPath)
	assert.Equal(t, "/var/lib/once/backups.staging", repo.stagingDir)
	assert.Equal(t, []string{"init", "--copy-chunker-params"}, repo.initArgs)
	assert.Equal(t, "id", repo.env["AWS_ACCESS_KEY_ID"])
	assert.Equal(t, "key", repo.env["AWS_SECRET_ACCESS_KEY"])
	assert.Equal(t, resticRepoMount, repo.env["RESTIC_FROM_REPOSITORY"])
	assert.Equal(t, "secret", repo.env["RESTIC_FROM_PASSWORD"])

	// The settings' own env map is never mutated.
	assert.NotContains(t, cfg.OffsiteEnv, "RESTIC_FROM_PASSWORD")
}

func TestResticForgetArgsFixedPolicy(t *testing.T) {
	args := resticForgetArgs("once")

	assert.Equal(t, []string{
		"forget", "--prune", "--host", "once",
		"--keep-daily", "7", "--keep-weekly", "4", "--keep-monthly", "6",
	}, args)
	assert.Equal(t, args, resticForgetArgs("once"))
}

func TestResticCheckArgsHaveNoReadData(t *testing.T) {
	args := resticCheckArgs()
	assert.Equal(t, []string{"check", "--json"}, args)
}

func TestBackupDiskCheck(t *testing.T) {
	// Zero baseline means a newly participating app: no basis for a check.
	assert.NoError(t, backupDiskCheck(100, 0))

	// The staging device must hold twice the last archive.
	assert.NoError(t, backupDiskCheck(2048, 1024))
	assert.Error(t, backupDiskCheck(2047, 1024))
}

func TestParseResticSummary(t *testing.T) {
	// restic --json streams ~10 status lines a second; only the final summary
	// line is the result.
	output := `{"message_type":"status","percent_done":0.5}
{"message_type":"status","percent_done":0.9}
{"message_type":"summary","snapshot_id":"abc123","total_bytes_processed":2048}`

	summary, ok := parseResticSummary(output)
	require.True(t, ok)
	assert.Equal(t, "abc123", summary.SnapshotID)
	assert.Equal(t, int64(2048), summary.TotalBytesProcessed)
}

func TestParseResticSummaryTakesLast(t *testing.T) {
	output := `{"message_type":"summary","snapshot_id":"first"}
{"message_type":"status"}
{"message_type":"summary","snapshot_id":"second"}`

	summary, ok := parseResticSummary(output)
	require.True(t, ok)
	assert.Equal(t, "second", summary.SnapshotID)
}

func TestParseResticSummaryMissing(t *testing.T) {
	_, ok := parseResticSummary(`{"message_type":"status"}` + "\nnot json")
	assert.False(t, ok)
}

func TestReclaimResticTmpFiles(t *testing.T) {
	repo := t.TempDir()
	packDir := filepath.Join(repo, "data", "a3")
	require.NoError(t, os.MkdirAll(packDir, 0o755))

	old := filepath.Join(packDir, "pack-tmp-123")
	fresh := filepath.Join(packDir, "pack-tmp-456")
	realPack := filepath.Join(packDir, "a3f2c1")
	for _, p := range []string{old, fresh, realPack} {
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o600))
	}
	stale := time.Now().Add(-resticTmpMaxAge - time.Hour)
	require.NoError(t, os.Chtimes(old, stale, stale))

	reclaimResticTmpFiles(repo)

	assert.NoFileExists(t, old, "orphaned temp files older than 24h are reclaimed")
	assert.FileExists(t, fresh, "recent temp files may belong to a live run")
	assert.FileExists(t, realPack, "pack files are never touched")
}

func TestResticCheckResultErrors(t *testing.T) {
	assert.NoError(t, resticCheckError(0, `{"message_type":"summary","num_errors":0}`))
	assert.Error(t, resticCheckError(1, `{"message_type":"summary","num_errors":2}`))
	assert.Error(t, resticCheckError(0, `{"message_type":"summary","num_errors":0,"suggest_repair_index":true}`))
	assert.Error(t, resticCheckError(0, `{"message_type":"summary","num_errors":0,"suggest_prune":true}`))
}
