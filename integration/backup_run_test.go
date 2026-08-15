package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/catalog"
	"github.com/basecamp/once/internal/docker"
	"github.com/basecamp/once/internal/system"
)

// The tracer's headline: the catalog Vaultwarden entry deploys healthy. Its
// Profile drives everything a Rails app got for free - the /data mount, the
// /alive health check, the DOMAIN env var, and the vaultwarden name.
func TestDeployVaultwardenFromCatalog(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	nsName := "once-vaultwarden-test"
	ns, err := docker.NewNamespace(nsName)
	require.NoError(t, err)
	// The CLI deploy below goes through its own namespace instance, so restore
	// before teardown or those containers would be missed.
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if restored, err := docker.RestoreNamespace(cleanupCtx, nsName); err == nil {
			restored.Teardown(cleanupCtx, true)
		}
	})

	require.NoError(t, ns.EnsureNetwork(ctx))
	proxyPorts := getProxyPorts(t)
	require.NoError(t, ns.Proxy().Boot(ctx, proxyPorts))

	entry := catalog.Resolve("vaultwarden")
	settings := docker.DefaultApplicationSettings()
	settings.Image = entry.ImageRef
	settings.Profile = entry.Profile
	settings.Host = "vaultwarden.localhost"

	app, _, err := ns.Deploy(ctx, settings, nil)
	require.NoError(t, err, "deploy must survive the /alive health check and verifyHTTP")

	assert.True(t, strings.HasPrefix(app.Settings.Name, "vaultwarden-"),
		"named from the Profile's BaseName, not the image ref's 'server'")

	containerName, err := app.ContainerName(ctx)
	require.NoError(t, err)

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	defer cli.Close()

	info, err := cli.ContainerInspect(ctx, containerName)
	require.NoError(t, err)

	require.Len(t, info.Mounts, 1, "a custom data path replaces both Rails mounts, never adds a third")
	assert.Equal(t, "/data", info.Mounts[0].Destination)

	env := inspectContainerEnv(t, ctx, containerName)
	assert.Contains(t, env, "DOMAIN="+app.URL(), "the Profile's URLEnvVar is filled with the app URL")

	// The same image as an arbitrary (non-catalog) deploy: the three Profile
	// flags stand in for the catalog entry.
	require.NoError(t, runCLI("-n", nsName, "deploy", "vaultwarden/server",
		"--data-path", "/data", "--health-check-path", "/alive", "--url-env-var", "DOMAIN"))

	restored, err := docker.RestoreNamespace(ctx, nsName)
	require.NoError(t, err)
	flagged := restored.ApplicationByHost("server.localhost")
	require.NotNil(t, flagged, "a custom image without BaseName is still named from the image ref")
	assert.Equal(t, docker.AppProfile{DataPath: "/data", HealthPath: "/alive", URLEnvVar: "DOMAIN"},
		flagged.Settings.Profile)
}

// An existing Rails-shaped app carries a zero Profile and must deploy exactly
// as before: both default mounts, /up health check, no URL env var.
func TestDeployZeroProfileKeepsRailsShape(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ns, err := docker.NewNamespace("once-zero-profile-test")
	require.NoError(t, err)
	defer ns.Teardown(ctx, true)

	require.NoError(t, ns.EnsureNetwork(ctx))
	require.NoError(t, ns.Proxy().Boot(ctx, getProxyPorts(t)))

	app := deployApp(t, ctx, ns, docker.ApplicationSettings{
		Name:  "railsapp",
		Image: "ghcr.io/basecamp/once-campfire:main",
		Host:  "railsapp.localhost",
	})

	containerName, err := app.ContainerName(ctx)
	require.NoError(t, err)

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	defer cli.Close()

	info, err := cli.ContainerInspect(ctx, containerName)
	require.NoError(t, err)

	var targets []string
	for _, m := range info.Mounts {
		targets = append(targets, m.Destination)
	}
	assert.ElementsMatch(t, []string{"/storage", "/rails/storage"}, targets)
}

// One scheduled run snapshots a due app into the local repo; a second run
// lands in the same --group-by host,paths retention group. This is ticket
// 02's trap: get the host or staging path wrong and retention silently keeps
// everything forever while every command reports success.
func TestBackupRunSnapshotsIntoOneRetentionGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	lockDir := t.TempDir()
	origLock := docker.BackupLockPath
	docker.BackupLockPath = filepath.Join(lockDir, "backup.lock")
	t.Cleanup(func() { docker.BackupLockPath = origLock })

	nsName := "once-backup-run-test"
	ns, err := docker.NewNamespace(nsName)
	require.NoError(t, err)
	defer ns.Teardown(ctx, true)

	require.NoError(t, ns.EnsureNetwork(ctx))
	require.NoError(t, ns.Proxy().Boot(ctx, getProxyPorts(t)))

	app := deployApp(t, ctx, ns, docker.ApplicationSettings{
		Name:   "backupapp",
		Image:  "ghcr.io/basecamp/once-campfire:main",
		Host:   "backupapp.localhost",
		Backup: docker.BackupSettings{AutoBackup: true},
	})

	containerName, err := app.ContainerName(ctx)
	require.NoError(t, err)
	execInContainer(t, ctx, containerName, []string{
		"sh", "-c", "echo 'vault data' > /rails/storage/vault.txt",
	})

	repoDir := filepath.Join(t.TempDir(), "backups")
	// restic runs as root, so the repo files it writes would defeat
	// t.TempDir()'s cleanup; hand ownership back before it runs.
	t.Cleanup(func() { chownToTestUser(t, repoDir) })
	password, err := docker.GenerateRepoPassword()
	require.NoError(t, err)
	require.NoError(t, ns.SetBackupRepoSettings(ctx, docker.BackupRepoSettings{
		Enabled:   true,
		LocalPath: repoDir,
		Password:  password,
	}))

	state, err := ns.LoadState(ctx)
	require.NoError(t, err)
	require.True(t, state.BackupDue("backupapp"))

	require.NoError(t, ns.RunBackups(ctx, state))

	// The run recorded its outcome, the archive baseline, and the repo check.
	state, err = ns.LoadState(ctx)
	require.NoError(t, err)
	result := state.AppState("backupapp").LastBackupResult()
	require.NotNil(t, result)
	assert.Empty(t, result.Error)
	assert.Positive(t, state.AppState("backupapp").LastArchiveBytes)
	assert.Empty(t, state.LastCheck["local"].Error)
	assert.NoFileExists(t, filepath.Join(repoDir+".staging", "backupapp.tar"),
		"the staging file is deleted after the local leg")

	// Second run: force due, then confirm both snapshots share one group.
	state.Apps["backupapp"].LastBackup.At = time.Now().Add(-25 * time.Hour)
	require.NoError(t, ns.RunBackups(ctx, state))

	groups := resticSnapshotGroups(t, ctx, repoDir, password)
	require.Len(t, groups, 1, "two runs must land in one host,paths retention group")
	assert.Equal(t, nsName, groups[0].GroupKey.Hostname, "the --host flag pins the hostname to the namespace")
	assert.Equal(t, []string{"/staging/backupapp.tar"}, groups[0].GroupKey.Paths)
	assert.Len(t, groups[0].Snapshots, 2)
}

// Enabling backups persists BackupRepoSettings on the proxy label through a
// real proxy roll; disabling withdraws only Enabled, keeping the password.
func TestBackupEnableDisableCLIPersistsSettings(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	nsName := "once-backup-enable-test"
	ns, err := docker.NewNamespace(nsName)
	require.NoError(t, err)
	defer ns.Teardown(ctx, true)

	require.NoError(t, ns.EnsureNetwork(ctx))
	require.NoError(t, ns.Proxy().Boot(ctx, getProxyPorts(t)))

	repoDir := filepath.Join(t.TempDir(), "backups")
	require.NoError(t, runCLI("-n", nsName, "backup", "enable", "--local-path", repoDir))

	restored, err := docker.RestoreNamespace(ctx, nsName)
	require.NoError(t, err)
	cfg := restored.BackupRepoSettings()
	assert.True(t, cfg.Enabled)
	assert.Equal(t, repoDir, cfg.LocalPath)
	assert.Regexp(t, `^[A-Z2-7]{32}$`, cfg.Password)

	require.NoError(t, runCLI("-n", nsName, "backup", "disable"))

	restored, err = docker.RestoreNamespace(ctx, nsName)
	require.NoError(t, err)
	disabled := restored.BackupRepoSettings()
	assert.False(t, disabled.Enabled)
	assert.Equal(t, cfg.Password, disabled.Password, "disable keeps the password - re-enabling must not mint a new one")
}

// The flock is honoured: a manual export during a run exits non-zero instead
// of pausing an already-paused container.
func TestManualExportRefusedWhileBackupLockHeld(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	origLock := docker.BackupLockPath
	docker.BackupLockPath = filepath.Join(t.TempDir(), "backup.lock")
	t.Cleanup(func() { docker.BackupLockPath = origLock })

	nsName := "once-backup-lock-test"
	ns, err := docker.NewNamespace(nsName)
	require.NoError(t, err)
	defer ns.Teardown(ctx, true)

	require.NoError(t, ns.EnsureNetwork(ctx))
	require.NoError(t, ns.Proxy().Boot(ctx, getProxyPorts(t)))

	deployApp(t, ctx, ns, docker.ApplicationSettings{
		Name:  "lockapp",
		Image: "ghcr.io/basecamp/once-campfire:main",
		Host:  "lockapp.localhost",
	})

	lock, err := system.TryLock(docker.BackupLockPath)
	require.NoError(t, err)
	defer lock.Unlock()

	exportFile := filepath.Join(t.TempDir(), "export.tar")
	err = runCLI("-n", nsName, "backup", "lockapp.localhost", exportFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "backup is already in progress")
	assert.NoFileExists(t, exportFile)

	lock.Unlock()
	err = runCLI("-n", nsName, "backup", "lockapp.localhost", exportFile)
	require.NoError(t, err, "the export succeeds once the lock is released")
	assert.FileExists(t, exportFile)
}

// Helpers

type resticSnapshotGroup struct {
	GroupKey struct {
		Hostname string   `json:"hostname"`
		Paths    []string `json:"paths"`
	} `json:"group_key"`
	Snapshots []struct {
		ShortID string `json:"short_id"`
	} `json:"snapshots"`
}

// resticSnapshotGroups lists the repo's snapshots grouped exactly as forget's
// retention would group them.
func resticSnapshotGroups(t *testing.T, ctx context.Context, repoDir, password string) []resticSnapshotGroup {
	t.Helper()

	out := runResticInTest(t, ctx, repoDir, password, "snapshots", "--group-by", "host,paths", "--json")

	var groups []resticSnapshotGroup
	require.NoError(t, json.Unmarshal([]byte(out), &groups), "restic output was: %s", out)
	return groups
}

// chownToTestUser hands a root-owned directory tree back to the test user so
// t.TempDir cleanup can remove it. Best effort: the dir may not exist when
// the test failed early.
func chownToTestUser(t *testing.T, dir string) {
	t.Helper()

	if _, err := os.Stat(dir); err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	_, _, _ = runContainerToCompletion(ctx,
		&container.Config{
			Image:      docker.ResticImage,
			Entrypoint: []string{"chown"},
			Cmd:        []string{"-R", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "/target"},
			User:       "0:0",
		},
		&container.HostConfig{
			Mounts: []mount.Mount{{Type: mount.TypeBind, Source: dir, Target: "/target"}},
		})
}

func runResticInTest(t *testing.T, ctx context.Context, repoDir, password string, args ...string) string {
	t.Helper()

	exitCode, stdout, err := runContainerToCompletion(ctx,
		&container.Config{
			Image: docker.ResticImage,
			Cmd:   args,
			Env:   []string{"RESTIC_REPOSITORY=/repo", "RESTIC_PASSWORD=" + password},
			User:  "0:0",
		},
		&container.HostConfig{
			Mounts: []mount.Mount{{Type: mount.TypeBind, Source: repoDir, Target: "/repo"}},
		})
	require.NoError(t, err)
	require.Zero(t, exitCode, "restic %v failed", args)
	return stdout
}

// runContainerToCompletion is the test-side create/start/wait/logs/remove
// shape shared by the restic helpers.
func runContainerToCompletion(ctx context.Context, config *container.Config, hostConfig *container.HostConfig) (int, string, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return 0, "", err
	}
	defer cli.Close()

	if _, err := cli.ImageInspect(ctx, config.Image); err != nil {
		reader, err := cli.ImagePull(ctx, config.Image, image.PullOptions{})
		if err != nil {
			return 0, "", err
		}
		_, _ = io.Copy(io.Discard, reader)
		reader.Close()
	}

	resp, err := cli.ContainerCreate(ctx, config, hostConfig, nil, nil, "")
	if err != nil {
		return 0, "", err
	}
	defer cli.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{Force: true})

	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return 0, "", err
	}

	waitCh, errCh := cli.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	var exitCode int
	select {
	case status := <-waitCh:
		exitCode = int(status.StatusCode)
	case err := <-errCh:
		return 0, "", err
	}

	logs, err := cli.ContainerLogs(ctx, resp.ID, container.LogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return exitCode, "", err
	}
	defer logs.Close()

	var stdout, stderr strings.Builder
	if _, err := stdcopy.StdCopy(&stdout, &stderr, logs); err != nil {
		return exitCode, "", err
	}
	return exitCode, stdout.String(), nil
}
