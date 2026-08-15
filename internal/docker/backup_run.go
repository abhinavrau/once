package docker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/basecamp/once/internal/fsutil"
	"github.com/basecamp/once/internal/system"
)

// BackupLockPath is the host flock both the scheduled Backup Run and manual
// `once backup` take, so racing pausers cannot tear a SQLite pair. A var so
// tests can point it at a writable location.
var BackupLockPath = "/var/lib/once/backup.lock"

// resticTmpMaxAge bounds the local -tmp- reclaim: a SIGKILLed run leaves
// temp files neither prune nor check can see, so anything older than this is
// dead space.
const resticTmpMaxAge = 24 * time.Hour

// RunBackups is one install-wide Backup Run: flock, reclaim local -tmp-
// orphans, then per due app stage-and-backup into the local repo and the
// offsite one, then one forget --prune and one check per repo. Runs before
// the update loop so the newest snapshot is of the known-good version.
func (n *Namespace) RunBackups(ctx context.Context, state *State) error {
	cfg := n.BackupRepoSettings()
	if !cfg.Enabled {
		return nil
	}

	var due []*Application
	for _, app := range n.applications {
		if app.Running && app.Settings.Backup.AutoBackup && state.BackupDue(app.Settings.Name) {
			due = append(due, app)
		}
	}
	if len(due) == 0 {
		return nil
	}

	lock, err := system.TryLock(BackupLockPath)
	if errors.Is(err, system.ErrLocked) {
		// Not a failure: nothing is recorded, the next tick retries.
		slog.Info("Skipping backup run: another backup is in progress")
		return nil
	}
	if err != nil {
		return fmt.Errorf("taking backup lock: %w", err)
	}
	defer lock.Unlock()

	reclaimResticTmpFiles(cfg.LocalPath)

	backedUp := 0
	for _, app := range due {
		name := app.Settings.Name
		size, err := n.backupApp(ctx, app, cfg, state.AppState(name))
		state.RecordBackup(name, err)
		if err != nil {
			slog.Error("Backup failed", "app", name, "error", err)
			continue
		}
		state.RecordArchiveBytes(name, size)
		backedUp++
		slog.Info("Backup completed", "app", name, "archiveBytes", size)
	}

	// Retention and verification run once per repo, and only when a backup
	// happened - with nothing due there is no run at all. Both are advisory:
	// the backups above are already safe.
	if backedUp > 0 {
		for _, repo := range configuredResticRepos(cfg) {
			n.maintainRepo(ctx, repo, state)
		}
	}

	if err := n.SaveState(ctx, state); err != nil {
		return fmt.Errorf("saving state after backup run: %w", err)
	}
	return nil
}

// Private

// backupApp runs one app's part of the Backup Run: free-disk check,
// pause-and-stage, restic backup of the Staging File to the local repo and
// then to the offsite one, and only then delete the Staging File. Returns the
// staged archive size - the next run's free-disk baseline.
func (n *Namespace) backupApp(ctx context.Context, app *Application, cfg BackupRepoSettings, appState *AppState) (int64, error) {
	stagingDir := cfg.StagingDir()
	if err := prepareBackupDirs(cfg); err != nil {
		return 0, err
	}

	free, err := system.FreeDisk(stagingDir)
	if err != nil {
		return 0, fmt.Errorf("checking free disk: %w", err)
	}
	var baseline int64
	if appState != nil {
		baseline = appState.LastArchiveBytes
	}
	if err := backupDiskCheck(free, baseline); err != nil {
		return 0, err
	}

	// The path is fixed forever: it is baked into every snapshot and forget
	// groups by it. The pause window is inside stageBackup - restic never
	// runs against a paused app.
	stagingFile := filepath.Join(stagingDir, app.Settings.Name+".tar")
	if err := app.stageBackup(ctx, stagingFile); err != nil {
		return 0, err
	}

	info, err := os.Stat(stagingFile)
	if err != nil {
		return 0, fmt.Errorf("sizing staging file: %w", err)
	}

	if err := n.backupToRepo(ctx, localResticRepo(cfg), app.Settings.Name); err != nil {
		return 0, err
	}

	// The offsite leg backs up the same Staging File - same --host, same
	// in-container path - so each repo holds one retention group per app, and
	// the two repos stay independent: a wedged lock or a full disk on one
	// cannot take the other down. It runs on every backup rather than only
	// when copying, so its failure is the common case with a flaky uplink,
	// not the exception: it logs and the run still succeeds. Failing the run
	// would retry hourly and re-pause the app over a network that will be
	// back in twenty minutes. The staleness signal is what surfaces this.
	if cfg.OffsiteURL != "" {
		if err := n.backupToRepo(ctx, offsiteResticRepo(cfg), app.Settings.Name); err != nil {
			slog.Error("Offsite backup failed; the local backup is intact and the run stands",
				"app", app.Settings.Name, "repository", cfg.OffsiteURL, "error", err)
		}
	}

	// Deleted only now: both legs read the same file. Advisory - the path is
	// fixed, so the next run overwrites it.
	if err := os.Remove(stagingFile); err != nil {
		slog.Warn("Failed to delete staging file", "file", stagingFile, "error", err)
	}

	return info.Size(), nil
}

// backupToRepo is one leg: ensure the repo exists, then back up the app's
// Staging File into it.
func (n *Namespace) backupToRepo(ctx context.Context, repo resticRepo, appName string) error {
	if err := n.ensureRepo(ctx, repo, nil); err != nil {
		return err
	}

	res, err := n.runResticRetryLock(ctx, repo, nil, resticBackupArgs(n.name, appName)...)
	if err != nil {
		return err
	}
	if res.exitCode != 0 {
		return fmt.Errorf("restic backup failed: %s", res.failure())
	}
	if summary, ok := parseResticSummary(res.stdout); ok {
		slog.Info("restic snapshot created", "app", appName, "snapshot", summary.SnapshotID, "repository", repo.displayName)
	}
	return nil
}

// maintainRepo is the per-repo tail of a Backup Run: retention, then
// verification. Both are advisory - the snapshots are already safe - and the
// check result is recorded rather than folded into the run's outcome, because
// "your repo is damaged" is a different emergency from "your backup did not
// run", and one failure flag for both loses that.
func (n *Namespace) maintainRepo(ctx context.Context, repo resticRepo, state *State) {
	if res, err := n.runResticRetryLock(ctx, repo, nil, resticForgetArgs(n.name)...); err != nil {
		slog.Error("restic forget failed", "repo", repo.name, "error", err)
	} else if res.exitCode != 0 {
		slog.Error("restic forget failed", "repo", repo.name, "error", res.failure())
	}

	checkErr := n.checkRepo(ctx, repo)
	state.RecordCheck(repo.name, checkErr)
	if checkErr != nil {
		slog.Error("restic check reported problems", "repo", repo.name, "error", checkErr)
	}
}

func (n *Namespace) checkRepo(ctx context.Context, repo resticRepo) error {
	res, err := n.runResticRetryLock(ctx, repo, nil, resticCheckArgs()...)
	if err != nil {
		return err
	}
	return resticCheckError(res.exitCode, res.stdout)
}

// stageBackup writes the app's backup archive to path. Unlike BackupToFile it
// records nothing in State: the Backup Run owns the outcome, which includes
// the restic leg that follows. On ErrUnpauseFailed the file is kept, matching
// the export path's semantics - the archive is intact and the emergency is
// the paused container.
func (a *Application) stageBackup(ctx context.Context, path string) error {
	file, err := fsutil.CreateFile(path)
	if err != nil {
		return fmt.Errorf("creating staging file: %w", err)
	}

	err = a.backupToWriter(ctx, file)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil && !errors.Is(err, ErrUnpauseFailed) {
		os.Remove(path)
	}
	return err
}

// Helpers

// prepareBackupDirs creates the host directories a run or a probe writes into:
// the Staging Directory, the local repo, and - when the offsite repository is
// a filesystem path rather than a backend URL - the offsite repo. Docker
// would create a missing bind source itself, but only after the caller has
// already lost the chance to report why it was missing.
func prepareBackupDirs(cfg BackupRepoSettings) error {
	dirs := []string{cfg.StagingDir(), cfg.LocalPath}
	if offsite := cfg.offsiteDir(); offsite != "" {
		dirs = append(dirs, offsite)
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	return nil
}

// reclaimResticTmpFiles deletes orphaned pack temp files from the local repo.
// A SIGKILLed restic leaves data/<xx>/*-tmp-* files that neither prune nor
// check can see, so without this the repo grows invisibly forever. Local repo
// only - Once has no filesystem reach into an offsite repo (a named, bounded
// ceiling). Runs under the flock, and only touches files older than 24h so a
// live run's temp files are never in reach.
func reclaimResticTmpFiles(localPath string) {
	matches, err := filepath.Glob(filepath.Join(localPath, "data", "*", "*-tmp-*"))
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-resticTmpMaxAge)
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(path); err != nil {
			slog.Warn("Failed to reclaim restic temp file", "file", path, "error", err)
		} else {
			slog.Info("Reclaimed orphaned restic temp file", "file", path)
		}
	}
}
