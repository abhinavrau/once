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

// The offsite leg. With an offsite repo configured, one run backs up the same
// Staging File to both repos, each repo keeps its own retention group for the
// app, and an offsite failure logs without failing the run. The offsite repo
// is a second local directory - the suite carries no cloud credentials.
func TestBackupRunOffsiteLeg(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	origLock := docker.BackupLockPath
	docker.BackupLockPath = filepath.Join(t.TempDir(), "backup.lock")
	t.Cleanup(func() { docker.BackupLockPath = origLock })

	nsName := "once-backup-offsite-test"
	ns, err := docker.NewNamespace(nsName)
	require.NoError(t, err)
	defer ns.Teardown(ctx, true)

	require.NoError(t, ns.EnsureNetwork(ctx))
	require.NoError(t, ns.Proxy().Boot(ctx, getProxyPorts(t)))

	deployApp(t, ctx, ns, docker.ApplicationSettings{
		Name:   "offsiteapp",
		Image:  "ghcr.io/basecamp/once-campfire:main",
		Host:   "offsiteapp.localhost",
		Backup: docker.BackupSettings{AutoBackup: true},
	})

	// restic runs as root, so hand the whole tree back before cleanup.
	dirs := t.TempDir()
	t.Cleanup(func() { chownToTestUser(t, dirs) })
	localRepo := filepath.Join(dirs, "backups")
	offsiteRepo := filepath.Join(dirs, "offsite")
	stagingFile := filepath.Join(localRepo+".staging", "offsiteapp.tar")

	password, err := docker.GenerateRepoPassword()
	require.NoError(t, err)
	require.NoError(t, ns.SetBackupRepoSettings(ctx, docker.BackupRepoSettings{
		Enabled:    true,
		LocalPath:  localRepo,
		OffsiteURL: offsiteRepo,
		Password:   password,
	}))

	state, err := ns.LoadState(ctx)
	require.NoError(t, err)
	require.NoError(t, ns.RunBackups(ctx, state))

	localGroups := resticSnapshotGroups(t, ctx, localRepo, password)
	offsiteGroups := resticSnapshotGroups(t, ctx, offsiteRepo, password)
	require.Len(t, localGroups, 1)
	require.Len(t, offsiteGroups, 1)
	assert.Len(t, offsiteGroups[0].Snapshots, 1, "one run, one snapshot in each repo")
	assert.Equal(t, localGroups[0].GroupKey, offsiteGroups[0].GroupKey,
		"the same --host and the same in-container path in both repos: one retention group per app per repo")
	assert.NoFileExists(t, stagingFile, "the Staging File is deleted only after the offsite leg returns")
	assert.Equal(t,
		resticChunkerPolynomial(t, ctx, localRepo, password),
		resticChunkerPolynomial(t, ctx, offsiteRepo, password),
		"offsite init passes --copy-chunker-params, which keeps a later `restic copy` backfill a one-shot")

	state, err = ns.LoadState(ctx)
	require.NoError(t, err)
	assert.Empty(t, state.AppState("offsiteapp").LastBackupResult().Error)
	for _, repo := range []string{docker.RepoNameLocal, docker.RepoNameOffsite} {
		assert.False(t, state.LastCheck[repo].At.IsZero(), "forget and check run once per repo: %s", repo)
		assert.Empty(t, state.LastCheck[repo].Error, repo)
	}

	// The staleness signal is a query, never state: erase State entirely and
	// the answer is unchanged.
	require.NoError(t, ns.SaveState(ctx, &docker.State{}))
	latest, err := ns.LatestOffsiteSnapshot(ctx, "offsiteapp")
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), latest, 10*time.Minute)

	// A second run: each repo keeps its own retention group, stable per app.
	state, err = ns.LoadState(ctx)
	require.NoError(t, err)
	require.NoError(t, ns.RunBackups(ctx, state))
	for name, dir := range map[string]string{"local": localRepo, "offsite": offsiteRepo} {
		groups := resticSnapshotGroups(t, ctx, dir, password)
		require.Len(t, groups, 1, "%s repo: both runs stay in one host,paths group", name)
		assert.Len(t, groups[0].Snapshots, 2, "%s repo", name)
	}

	// An offsite repo Once cannot open: the leg fails, and the run stands.
	foreignRepo := filepath.Join(dirs, "foreign")
	require.NoError(t, os.MkdirAll(foreignRepo, 0o755))
	runResticInTest(t, ctx, foreignRepo, "not-the-generated-password", "init")

	cfg := ns.BackupRepoSettings()
	cfg.OffsiteURL = foreignRepo
	require.NoError(t, ns.SetBackupRepoSettings(ctx, cfg))

	state, err = ns.LoadState(ctx)
	require.NoError(t, err)
	state.Apps["offsiteapp"].LastBackup.At = time.Now().Add(-25 * time.Hour)
	require.NoError(t, ns.RunBackups(ctx, state), "an offsite failure never fails the run")

	state, err = ns.LoadState(ctx)
	require.NoError(t, err)
	result := state.AppState("offsiteapp").LastBackupResult()
	require.NotNil(t, result)
	assert.Empty(t, result.Error, "the run is recorded successful")
	assert.Empty(t, state.LastCheck[docker.RepoNameLocal].Error)
	assert.NotEmpty(t, state.LastCheck[docker.RepoNameOffsite].Error,
		"a damaged repo alarms through check, separately from the run result")

	localGroups = resticSnapshotGroups(t, ctx, localRepo, password)
	require.Len(t, localGroups, 1)
	assert.WithinDuration(t, time.Now(), newestSnapshotTime(t, localGroups[0]), 10*time.Minute,
		"the local snapshot is intact")
	assert.NoFileExists(t, stagingFile)
	assert.Empty(t, resticSnapshotGroups(t, ctx, foreignRepo, "not-the-generated-password"),
		"nothing landed in the repo Once could not open")

	_, err = ns.LatestOffsiteSnapshot(ctx, "offsiteapp")
	assert.Error(t, err, "staleness reports what the repo says, and says so when it cannot ask")
}

// Enabling backups runs the validation probe (initializing the repo and
// leaving no snapshot behind), prints the no-TTY banner without prompting,
// and persists BackupRepoSettings on the proxy label through a real proxy
// roll; disabling withdraws only Enabled, keeping the password.
func TestBackupEnableDisableCLIPersistsSettings(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	nsName := "once-backup-enable-test"
	ns, err := docker.NewNamespace(nsName)
	require.NoError(t, err)
	defer ns.Teardown(ctx, true)

	require.NoError(t, ns.EnsureNetwork(ctx))
	require.NoError(t, ns.Proxy().Boot(ctx, getProxyPorts(t)))

	repoDir := filepath.Join(t.TempDir(), "backups")
	t.Cleanup(func() { chownToTestUser(t, repoDir) })
	require.NoError(t, runCLI("-n", nsName, "backup", "enable", "--local-path", repoDir))

	restored, err := docker.RestoreNamespace(ctx, nsName)
	require.NoError(t, err)
	cfg := restored.BackupRepoSettings()
	assert.True(t, cfg.Enabled)
	assert.Equal(t, repoDir, cfg.LocalPath)
	assert.Regexp(t, `^[A-Z2-7]{32}$`, cfg.Password)

	// The probe initialized the repo and forgot its own snapshot.
	assert.FileExists(t, filepath.Join(repoDir, "config"), "the probe initializes the repo")
	assert.Empty(t, resticSnapshotGroups(t, ctx, repoDir, cfg.Password), "the probe leaves no snapshot behind")

	require.NoError(t, runCLI("-n", nsName, "backup", "disable"))

	restored, err = docker.RestoreNamespace(ctx, nsName)
	require.NoError(t, err)
	disabled := restored.BackupRepoSettings()
	assert.False(t, disabled.Enabled)
	assert.Equal(t, cfg.Password, disabled.Password, "disable keeps the password - re-enabling must not mint a new one")
}

// A failed probe persists everything including the password, withholds only
// Enabled, and surfaces in status with restic's error; the retry reuses the
// persisted password, never regenerating it. The failure is a local repo
// initialized under a different password: restic's wrong-password exit fails
// fast, with no backend retry backoff.
func TestBackupEnableProbeFailureWithholdsEnabled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	nsName := "once-backup-probe-test"
	ns, err := docker.NewNamespace(nsName)
	require.NoError(t, err)
	defer ns.Teardown(ctx, true)

	require.NoError(t, ns.EnsureNetwork(ctx))
	require.NoError(t, ns.Proxy().Boot(ctx, getProxyPorts(t)))

	foreignRepo := filepath.Join(t.TempDir(), "foreign")
	require.NoError(t, os.MkdirAll(foreignRepo, 0o755))
	t.Cleanup(func() { chownToTestUser(t, foreignRepo) })
	runResticInTest(t, ctx, foreignRepo, "not-the-generated-password", "init")

	err = runCLI("-n", nsName, "backup", "enable", "--local-path", foreignRepo)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wrong password", "the restic error is reported verbatim")

	restored, err := docker.RestoreNamespace(ctx, nsName)
	require.NoError(t, err)
	cfg := restored.BackupRepoSettings()
	assert.False(t, cfg.Enabled, "Enabled is withheld on probe failure")
	assert.Equal(t, foreignRepo, cfg.LocalPath)
	require.Regexp(t, `^[A-Z2-7]{32}$`, cfg.Password, "the password persists so a retry reuses it")

	state, err := restored.LoadState(ctx)
	require.NoError(t, err)
	probe := state.LastProbeResult()
	require.NotNil(t, probe)
	assert.NotEmpty(t, probe.Error)

	status, err := captureCLIOutput("-n", nsName, "backup", "status")
	require.NoError(t, err)
	assert.Contains(t, status, "configured but not enabled")
	assert.Contains(t, status, "Last validation: failed")

	// Retry against a fresh path: the persisted password is reused - the
	// repo it initializes now is openable on the next enable - and only now
	// does Enabled flip.
	freshRepo := filepath.Join(t.TempDir(), "backups")
	t.Cleanup(func() { chownToTestUser(t, freshRepo) })
	require.NoError(t, runCLI("-n", nsName, "backup", "enable", "--local-path", freshRepo))

	restored, err = docker.RestoreNamespace(ctx, nsName)
	require.NoError(t, err)
	enabled := restored.BackupRepoSettings()
	assert.True(t, enabled.Enabled)
	assert.Equal(t, freshRepo, enabled.LocalPath)
	assert.Equal(t, cfg.Password, enabled.Password, "retry reuses the persisted password")
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
		ShortID string    `json:"short_id"`
		Time    time.Time `json:"time"`
	} `json:"snapshots"`
}

func newestSnapshotTime(t *testing.T, group resticSnapshotGroup) time.Time {
	t.Helper()

	require.NotEmpty(t, group.Snapshots)
	var newest time.Time
	for _, snapshot := range group.Snapshots {
		if snapshot.Time.After(newest) {
			newest = snapshot.Time
		}
	}
	return newest
}

// resticChunkerPolynomial reads the repo's content-defined chunking
// parameter. Two repos share it only when one was initialized from the other
// with --copy-chunker-params.
func resticChunkerPolynomial(t *testing.T, ctx context.Context, repoDir, password string) string {
	t.Helper()

	var config struct {
		ChunkerPolynomial string `json:"chunker_polynomial"`
	}
	out := runResticInTest(t, ctx, repoDir, password, "cat", "config")
	require.NoError(t, json.Unmarshal([]byte(out), &config), "restic output was: %s", out)
	require.NotEmpty(t, config.ChunkerPolynomial)
	return config.ChunkerPolynomial
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

// stageBackupDirs gives a test a writable home for everything a Backup Run and
// a restore reach for outside the namespace: the host flock and the default
// local repo both live under /var/lib/once on a real install, which the suite
// cannot create. Returns the directory the test hangs its repos off; restic
// writes into it as root, so it is handed back before cleanup.
func stageBackupDirs(t *testing.T) string {
	t.Helper()

	origLock := docker.BackupLockPath
	docker.BackupLockPath = filepath.Join(t.TempDir(), "backup.lock")
	t.Cleanup(func() { docker.BackupLockPath = origLock })

	dirs := t.TempDir()
	t.Cleanup(func() { chownToTestUser(t, dirs) })

	origDefault := docker.DefaultBackupLocalPath
	docker.DefaultBackupLocalPath = filepath.Join(dirs, "default")
	t.Cleanup(func() { docker.DefaultBackupLocalPath = origDefault })

	return dirs
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
