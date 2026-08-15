package integration

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/catalog"
	"github.com/basecamp/once/internal/docker"
)

// Bare-metal recovery, end to end: an install backs Vaultwarden up to two
// repos and is then destroyed, and a second install - holding nothing but the
// repo path and the password - lists what the repo holds, restores the newest
// snapshot, refuses to collide with it, restores an older one in its place,
// and resumes backups to the repo it recovered from.
//
// The second install's proxy is booted on free ports rather than left to
// ns.Setup: the suite cannot bind 80 and 443. Everything else about the
// destination is what a fresh machine has - no applications, no backup
// settings, no local repo.
func TestRestoreVaultwardenFromRepo(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	origLock := docker.BackupLockPath
	docker.BackupLockPath = filepath.Join(t.TempDir(), "backup.lock")
	t.Cleanup(func() { docker.BackupLockPath = origLock })

	dirs := t.TempDir()
	t.Cleanup(func() { chownToTestUser(t, dirs) })
	localRepo := filepath.Join(dirs, "backups")
	offsiteRepo := filepath.Join(dirs, "offsite")

	// The restored install stages into the default install path, which only
	// root can create.
	origDefault := docker.DefaultBackupLocalPath
	docker.DefaultBackupLocalPath = filepath.Join(dirs, "default")
	t.Cleanup(func() { docker.DefaultBackupLocalPath = origDefault })

	password, err := docker.GenerateRepoPassword()
	require.NoError(t, err)

	sourceName := "once-restore-repo-src"
	source, err := docker.NewNamespace(sourceName)
	require.NoError(t, err)
	sourceTornDown := false
	defer func() {
		if !sourceTornDown {
			source.Teardown(ctx, true)
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
	require.NoError(t, err)
	require.NoError(t, source.Refresh(ctx))
	appName := app.Settings.Name
	app = source.Application(appName)
	require.NotNil(t, app)

	require.NoError(t, source.SetBackupRepoSettings(ctx, docker.BackupRepoSettings{
		Enabled:    true,
		LocalPath:  localRepo,
		OffsiteURL: offsiteRepo,
		Password:   password,
	}))

	// Two snapshots holding different vault data, so restoring an older one is
	// provable rather than merely reported.
	writeVaultData(t, ctx, app, "vault-data-one")
	state, err := source.LoadState(ctx)
	require.NoError(t, err)
	require.NoError(t, source.RunBackups(ctx, state))

	writeVaultData(t, ctx, app, "vault-data-two")
	state, err = source.LoadState(ctx)
	require.NoError(t, err)
	state.Apps[appName].LastBackup.At = time.Now().Add(-25 * time.Hour)
	require.NoError(t, source.RunBackups(ctx, state))

	// A configured install reads its own local repo, named in the listing.
	listing, err := captureCLIOutput("-n", sourceName, "restore")
	require.NoError(t, err)
	assert.Contains(t, listing, "Applications in "+localRepo)
	assert.NotContains(t, listing, offsiteRepo, "the configured default is the local repo, never the offsite copy")
	assert.Contains(t, listing, appName)
	assert.Contains(t, listing, "2 snapshots")
	assert.Contains(t, listing, entry.ImageRef)

	// And never falls back to the offsite copy when the local one is gone:
	// silently reading the other copy would hide which was trusted.
	cfg := source.BackupRepoSettings()
	cfg.LocalPath = filepath.Join(dirs, "vanished")
	require.NoError(t, source.SetBackupRepoSettings(ctx, cfg))
	_, err = captureCLIOutput("-n", sourceName, "restore", appName)
	require.Error(t, err)
	assert.Contains(t, err.Error(), filepath.Join(dirs, "vanished"))
	assert.NotContains(t, err.Error(), offsiteRepo)

	// The host is gone. Everything below has only the repo and the password.
	require.NoError(t, source.Teardown(ctx, true))
	sourceTornDown = true

	destName := "once-restore-repo-dst"
	dest, err := docker.NewNamespace(destName)
	require.NoError(t, err)
	defer dest.Teardown(ctx, true)
	require.NoError(t, dest.EnsureNetwork(ctx))
	require.NoError(t, dest.Proxy().Boot(ctx, getProxyPorts(t)))
	require.Empty(t, dest.Applications())
	require.False(t, dest.BackupRepoSettings().Configured(), "a fresh install has no backup configuration at all")

	t.Setenv("RESTIC_PASSWORD", password)

	// Discovery before trust: what does this repo hold?
	listing, err = captureCLIOutput("-n", destName, "restore", "--repo", offsiteRepo)
	require.NoError(t, err)
	assert.Contains(t, listing, "Applications in "+offsiteRepo)
	assert.Contains(t, listing, appName)
	assert.Contains(t, listing, "2 snapshots")
	assert.Contains(t, listing, entry.ImageRef)

	history, err := captureCLIOutput("-n", destName, "restore", appName, "--repo", offsiteRepo, "--list")
	require.NoError(t, err)
	ids := regexp.MustCompile(`(?m)^  ([0-9a-f]{8}) `).FindAllStringSubmatch(history, -1)
	require.Len(t, ids, 2, "history was: %s", history)
	olderID, newerID := ids[0][1], ids[1][1]

	// The newest snapshot, restored onto a machine that has never seen it.
	out, err := captureCLIOutput("-n", destName, "restore", appName, "--repo", offsiteRepo)
	require.NoError(t, err)
	assert.Contains(t, out, "Restoring "+appName+" from "+offsiteRepo)
	assert.Contains(t, out, "snapshot "+newerID+" taken ")
	assert.Contains(t, out, "on host "+sourceName, "the pick names the install that is gone")
	assert.Contains(t, out, "image "+entry.ImageRef)
	assert.Regexp(t, `Restored vaultwarden-\w+ from snapshot `+newerID+` \(\d{4}-\d{2}-\d{2}, `+regexp.QuoteMeta(entry.ImageRef)+`\)`, out)
	assert.Contains(t, out, "Run `once backup enable --offsite-url "+offsiteRepo+"`",
		"with no terminal to ask on, the resume-backups prompt becomes its one-liner")

	dest, err = docker.RestoreNamespace(ctx, destName)
	require.NoError(t, err)
	restored := dest.ApplicationByHost("vaultwarden.localhost")
	require.NotNil(t, restored)
	assert.True(t, strings.HasPrefix(restored.Settings.Name, "vaultwarden-"))
	assert.Equal(t, entry.Profile, restored.Settings.Profile,
		"the App Profile round-trips in once.application.json: data path, health path, and URL env var")
	assert.True(t, restored.Settings.Backup.AutoBackup)
	assertSingleDataMount(t, ctx, restored, "/data")
	assert.Equal(t, "vault-data-two", readVaultData(t, ctx, restored))

	// A second restore of the same snapshot collides on the hostname. The
	// refusal names the occupant and how to clear it, and leaves it alone.
	_, err = captureCLIOutput("-n", destName, "restore", appName, "--repo", offsiteRepo)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hostname already in use")
	assert.Contains(t, err.Error(), restored.Settings.Name+" already serves vaultwarden.localhost")
	assert.Contains(t, err.Error(), "once remove vaultwarden.localhost")

	dest, err = docker.RestoreNamespace(ctx, destName)
	require.NoError(t, err)
	require.Len(t, dest.Applications(), 1, "a refused restore leaves the occupant untouched")
	assert.Equal(t, "vault-data-two", readVaultData(t, ctx, dest.Application(restored.Settings.Name)))

	// Clear the host and restore the older snapshot in its place.
	require.NoError(t, dest.Application(restored.Settings.Name).Remove(ctx, true))
	dest, err = docker.RestoreNamespace(ctx, destName)
	require.NoError(t, err)

	out, err = captureCLIOutput("-n", destName, "restore", appName, "--repo", offsiteRepo, "--snapshot", olderID)
	require.NoError(t, err)
	assert.Contains(t, out, "snapshot "+olderID+" taken ")
	assert.Contains(t, out, "Restored ")

	dest, err = docker.RestoreNamespace(ctx, destName)
	require.NoError(t, err)
	older := dest.ApplicationByHost("vaultwarden.localhost")
	require.NotNil(t, older)
	assert.Equal(t, "vault-data-one", readVaultData(t, ctx, older), "--snapshot restores the older vault, not the newest")

	// Resuming backups: the repo just recovered from becomes the Offsite Repo,
	// and the fresh local one copies its chunker parameters so a later
	// `restic copy` backfill stays a one-shot.
	resumedLocal := filepath.Join(dirs, "resumed")
	require.NoError(t, dest.ResumeBackupsAfterRestore(ctx, docker.BackupRepoSettings{
		LocalPath:  resumedLocal,
		OffsiteURL: offsiteRepo,
		Password:   password,
	}), "the probe must pass against both repos")
	require.NoError(t, dest.EnableBackups(ctx))

	assert.Equal(t,
		resticChunkerPolynomial(t, ctx, offsiteRepo, password),
		resticChunkerPolynomial(t, ctx, resumedLocal, password),
		"the local repo is initialized with --copy-chunker-params from the remote")

	dest, err = docker.RestoreNamespace(ctx, destName)
	require.NoError(t, err)
	assert.True(t, dest.BackupRepoSettings().Enabled)

	state, err = dest.LoadState(ctx)
	require.NoError(t, err)
	require.NoError(t, dest.RunBackups(ctx, state))

	restoredName := older.Settings.Name
	state, err = dest.LoadState(ctx)
	require.NoError(t, err)
	require.NotNil(t, state.AppState(restoredName).LastBackupResult())
	assert.Empty(t, state.AppState(restoredName).LastBackupResult().Error)
	for name, dir := range map[string]string{"resumed local": resumedLocal, "offsite": offsiteRepo} {
		assert.Contains(t, resticSnapshotAppTags(t, ctx, dir, password), "app:"+restoredName,
			"the recovered install backs up to the %s repo", name)
	}
}

// Helpers

func writeVaultData(t *testing.T, ctx context.Context, app *docker.Application, content string) {
	t.Helper()

	containerName, err := app.ContainerName(ctx)
	require.NoError(t, err)
	execInContainer(t, ctx, containerName, []string{"sh", "-c", fmt.Sprintf("printf %%s %s > /data/vault.txt", content)})
}

func readVaultData(t *testing.T, ctx context.Context, app *docker.Application) string {
	t.Helper()

	require.NotNil(t, app)
	containerName, err := app.ContainerName(ctx)
	require.NoError(t, err)
	return strings.TrimSpace(execInContainerOutput(t, ctx, containerName, []string{"cat", "/data/vault.txt"}))
}

func assertSingleDataMount(t *testing.T, ctx context.Context, app *docker.Application, target string) {
	t.Helper()

	containerName, err := app.ContainerName(ctx)
	require.NoError(t, err)

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	defer cli.Close()

	info, err := cli.ContainerInspect(ctx, containerName)
	require.NoError(t, err)
	require.Len(t, info.Mounts, 1, "the Profile's data path replaces both Rails mounts")
	assert.Equal(t, target, info.Mounts[0].Destination)
}

// resticSnapshotAppTags lists the app tags a repo holds - the tags a restore
// reads app names out of.
func resticSnapshotAppTags(t *testing.T, ctx context.Context, repoDir, password string) []string {
	t.Helper()

	out := runResticInTest(t, ctx, repoDir, password, "snapshots", "--json", "--no-lock")
	var tags []string
	for _, match := range regexp.MustCompile(`"app:[^"]+"`).FindAllString(out, -1) {
		tags = append(tags, strings.Trim(match, `"`))
	}
	return tags
}

// execInContainerOutput is execInContainer with the command's stdout returned
// rather than discarded: proving a vault came back means reading it.
func execInContainerOutput(t *testing.T, ctx context.Context, containerName string, cmd []string) string {
	t.Helper()

	c, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	defer c.Close()

	execResp, err := c.ContainerExecCreate(ctx, containerName, container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	})
	require.NoError(t, err)

	resp, err := c.ContainerExecAttach(ctx, execResp.ID, container.ExecStartOptions{})
	require.NoError(t, err)
	defer resp.Close()

	var stdout, stderr strings.Builder
	_, err = stdcopy.StdCopy(&stdout, &stderr, resp.Reader)
	require.NoError(t, err)

	inspect, err := c.ContainerExecInspect(ctx, execResp.ID)
	require.NoError(t, err)
	require.Equal(t, 0, inspect.ExitCode, "exec command failed: %s", stderr.String())
	return stdout.String()
}
