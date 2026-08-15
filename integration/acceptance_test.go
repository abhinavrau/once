package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/catalog"
	"github.com/basecamp/once/internal/docker"
)

// The whole journey, on the real thing: Vaultwarden deploys from the catalog,
// a person puts real vault data in it through the Bitwarden API their client
// speaks, backups are enabled through `once backup enable` - probe, kit and
// all - and a Backup Run lands the vault in two repos. Then the host is
// destroyed, and a machine holding nothing but the offsite repo's path and the
// password from the printed kit brings the vault back and reads every item out
// of it again, attachment bytes included.
//
// The second directory stands in for offsite (no cloud credentials in the
// suite), which is a genuine restic backend rather than a stub: a filesystem
// path is a repository, so the leg exercised here is the leg an S3 URL takes.
func TestVaultwardenBackupAndBareMetalRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	origLock := docker.BackupLockPath
	docker.BackupLockPath = filepath.Join(t.TempDir(), "backup.lock")
	t.Cleanup(func() { docker.BackupLockPath = origLock })

	// The restore stages into the default install path, which only root can
	// create.
	dirs := t.TempDir()
	t.Cleanup(func() { chownToTestUser(t, dirs) })
	origDefault := docker.DefaultBackupLocalPath
	docker.DefaultBackupLocalPath = filepath.Join(dirs, "default")
	t.Cleanup(func() { docker.DefaultBackupLocalPath = origDefault })

	localRepo := filepath.Join(dirs, "backups")
	offsiteRepo := filepath.Join(dirs, "offsite")

	// -- The install that holds the vault --

	sourceName := "once-acceptance-src"
	source, err := docker.NewNamespace(sourceName)
	require.NoError(t, err)
	sourceTornDown := false
	// `backup enable` rolls the proxy through its own namespace instance, so
	// tear down off a freshly read namespace or that container is missed.
	defer func() {
		if !sourceTornDown {
			if reread, err := docker.RestoreNamespace(context.Background(), sourceName); err == nil {
				reread.Teardown(context.Background(), true)
			}
		}
	}()

	require.NoError(t, source.EnsureNetwork(ctx))
	require.NoError(t, source.Proxy().Boot(ctx, getProxyPorts(t)))

	entry := catalog.Resolve("vaultwarden")
	settings := docker.DefaultApplicationSettings()
	settings.Image = entry.ImageRef
	settings.Profile = entry.Profile
	settings.Host = "vaultwarden.localhost"
	settings.Backup = docker.BackupSettings{AutoBackup: true}

	app, _, err := source.Deploy(ctx, settings, nil)
	require.NoError(t, err, "the deploy gates are readiness on /alive and a 2xx from verifyHTTP")
	appName := app.Settings.Name

	// -- Vault data, written the way a Bitwarden client writes it --

	const email = "ada@example.com"
	const masterPassword = "once-acceptance-master-password"

	vault := newVaultwardenClient(t, app.URL())
	vault.Register(email, masterPassword)
	vault.LogIn(email, masterPassword)
	written := []vaultItem{
		vault.AddItem("Bank", "ada@example.com", "correct-horse-battery-staple", "recovery-codes.txt", acceptanceAttachment()),
	}
	require.Equal(t, written, vault.Items(), "the vault holds what was just written")

	// -- Backups enabled through the CLI: probe, then the recovery kit --

	out, err := captureCLIOutput("-n", sourceName, "backup", "enable",
		"--local-path", localRepo, "--offsite-url", offsiteRepo)
	require.NoError(t, err, "enable runs the validation probe against both repos")
	assert.Contains(t, out, "BACKUP PASSWORD - SHOWN ONCE, NEVER AGAIN")
	assert.Contains(t, out, "Offsite repo: "+offsiteRepo)

	source, err = docker.RestoreNamespace(ctx, sourceName)
	require.NoError(t, err)
	password := source.BackupRepoSettings().Password
	require.NotEmpty(t, password)
	assert.Equal(t, password, docker.NormalizeRepoPassword(kitPassword(t, out)),
		"the password on the printed kit is the one the repos were initialized with - it is all the operator carries off this machine")

	// kamal-proxy's routes live in its named volume, so the label rewrite that
	// stores the settings leaves the vault served.
	require.Equal(t, written, vault.Items(), "the app is still served after the proxy roll")

	// -- One Backup Run, two repos --

	state, err := source.LoadState(ctx)
	require.NoError(t, err)
	require.True(t, state.BackupDue(appName))
	require.NoError(t, source.RunBackups(ctx, state))

	local := resticSnapshotList(t, ctx, localRepo, password)
	offsite := resticSnapshotList(t, ctx, offsiteRepo, password)
	require.Len(t, local, 1, "one run, one snapshot in the local repo")
	require.Len(t, offsite, 1, "one run, one snapshot in the offsite repo")
	assert.Equal(t, local[0].Tree, offsite[0].Tree, "both repos hold the same vault")
	assert.Empty(t, offsite[0].Original,
		"restic copy stamps the source snapshot's id into `original`; the offsite copy is a second `backup` of the same Staging File, so it has none")
	assert.True(t, offsite[0].Time.After(local[0].Time),
		"a copy carries the original's timestamp; a second `backup` leg takes its own, after the local one finished")
	assert.Equal(t, local[0].Paths, offsite[0].Paths)
	assert.Equal(t, sourceName, offsite[0].Hostname)

	state, err = source.LoadState(ctx)
	require.NoError(t, err)
	require.NotNil(t, state.AppState(appName).LastBackupResult())
	assert.Empty(t, state.AppState(appName).LastBackupResult().Error)

	// -- The host is gone --

	require.NoError(t, source.Teardown(ctx, true))
	sourceTornDown = true

	// -- Bare metal: a fresh machine, the repo path, and the password --

	destName := "once-acceptance-dst"
	dest, err := docker.NewNamespace(destName)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if reread, err := docker.RestoreNamespace(cleanupCtx, destName); err == nil {
			reread.Teardown(cleanupCtx, true)
		}
	})
	require.NoError(t, dest.EnsureNetwork(ctx))
	require.NoError(t, dest.Proxy().Boot(ctx, getProxyPorts(t)))
	require.Empty(t, dest.Applications())
	require.False(t, dest.BackupRepoSettings().Configured(), "nothing here knows about any repo")

	t.Setenv("RESTIC_PASSWORD", password)

	listing, err := captureCLIOutput("-n", destName, "restore", "--repo", offsiteRepo)
	require.NoError(t, err)
	assert.Contains(t, listing, "Applications in "+offsiteRepo)
	assert.Contains(t, listing, appName)
	assert.Contains(t, listing, entry.ImageRef)

	out, err = captureCLIOutput("-n", destName, "restore", appName, "--repo", offsiteRepo)
	require.NoError(t, err, "the restore's own deploy waits on /alive, so a vault that will not come up is a failed restore")
	assert.Contains(t, out, "Restored ")

	dest, err = docker.RestoreNamespace(ctx, destName)
	require.NoError(t, err)
	restored := dest.ApplicationByHost("vaultwarden.localhost")
	require.NotNil(t, restored)

	// The Profile travelled in once.application.json, so the recovered app is
	// shaped like a Vaultwarden rather than like a Rails app.
	assert.True(t, strings.HasPrefix(restored.Settings.Name, "vaultwarden-"),
		"named from the Profile's BaseName, not the image ref's 'server'")
	assert.Equal(t, entry.Profile, restored.Settings.Profile)
	assertSingleDataMount(t, ctx, restored, "/data")
	restoredContainer, err := restored.ContainerName(ctx)
	require.NoError(t, err)
	assert.Contains(t, inspectContainerEnv(t, ctx, restoredContainer), "DOMAIN="+restored.URL(),
		"the URL env var is recomputed for this install, never restored from the old one")

	resp, err := http.Get(restored.URL() + entry.Profile.HealthCheckPath())
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "the recovered vault answers on /alive through this install's proxy")

	// -- The vault itself, read back through the API --

	recovered := newVaultwardenClient(t, restored.URL())
	recovered.LogIn(email, masterPassword)
	assert.Equal(t, written, recovered.Items(),
		"every item comes back byte for byte, attachment included")
}

// Helpers

// acceptanceAttachment is a payload big enough to be a real file and
// incompressible enough that a truncated restore cannot pass by luck.
func acceptanceAttachment() []byte {
	data := make([]byte, 4096)
	for i := range data {
		data[i] = byte(i*i*7 + i*13 + 3)
	}
	return data
}

// kitPassword reads the password out of the printed Recovery Kit, in the
// grouped form the operator copies down.
func kitPassword(t *testing.T, banner string) string {
	t.Helper()

	match := regexp.MustCompile(`Password:\s+(\S+)`).FindStringSubmatch(banner)
	require.Len(t, match, 2, "no password in the printed kit: %s", banner)
	return match[1]
}

// resticSnapshotList is the flat snapshot listing, with the fields that tell a
// second `backup` apart from a `restic copy`.
func resticSnapshotList(t *testing.T, ctx context.Context, repoDir, password string) []resticSnapshot {
	t.Helper()

	out := runResticInTest(t, ctx, repoDir, password, "snapshots", "--json", "--no-lock")
	var snapshots []resticSnapshot
	require.NoError(t, json.Unmarshal([]byte(out), &snapshots), "restic snapshots output was: %s", out)
	return snapshots
}

type resticSnapshot struct {
	ShortID  string    `json:"short_id"`
	Time     time.Time `json:"time"`
	Tree     string    `json:"tree"`
	Paths    []string  `json:"paths"`
	Hostname string    `json:"hostname"`
	Tags     []string  `json:"tags"`
	Original string    `json:"original"`
}
