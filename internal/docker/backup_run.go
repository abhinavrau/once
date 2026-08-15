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
// orphans, then per due app stage-and-backup into the local repo, then one
// forget --prune and one check for the repo. Runs before the update loop so
// the newest snapshot is of the known-good version. The offsite leg lands
// with issue 15.
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
		size, err := n.backupAppToLocalRepo(ctx, app, cfg, state.AppState(name))
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
		repo := localResticRepo(cfg)
		if res, err := n.runResticRetryLock(ctx, cfg.StagingDir(), repo, resticForgetArgs(n.name)...); err != nil {
			slog.Error("restic forget failed", "error", err)
		} else if res.exitCode != 0 {
			slog.Error("restic forget failed", "error", res.failure())
		}

		checkErr := n.checkRepo(ctx, cfg.StagingDir(), repo)
		state.RecordCheck("local", checkErr)
		if checkErr != nil {
			slog.Error("restic check reported problems", "repo", "local", "error", checkErr)
		}
	}

	if err := n.SaveState(ctx, state); err != nil {
		return fmt.Errorf("saving state after backup run: %w", err)
	}
	return nil
}

// Private

// backupAppToLocalRepo runs one app's leg of the Backup Run: free-disk check,
// pause-and-stage, restic backup to the local repo, delete the Staging File.
// Returns the staged archive size - the next run's free-disk baseline.
func (n *Namespace) backupAppToLocalRepo(ctx context.Context, app *Application, cfg BackupRepoSettings, appState *AppState) (int64, error) {
	stagingDir := cfg.StagingDir()
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return 0, fmt.Errorf("creating staging directory: %w", err)
	}
	if err := os.MkdirAll(cfg.LocalPath, 0o700); err != nil {
		return 0, fmt.Errorf("creating repository directory: %w", err)
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

	repo := localResticRepo(cfg)
	if err := n.ensureRepo(ctx, stagingDir, repo); err != nil {
		return 0, err
	}
	res, err := n.runResticRetryLock(ctx, stagingDir, repo, resticBackupArgs(n.name, app.Settings.Name)...)
	if err != nil {
		return 0, err
	}
	if res.exitCode != 0 {
		return 0, fmt.Errorf("restic backup failed: %s", res.failure())
	}
	if summary, ok := parseResticSummary(res.stdout); ok {
		slog.Info("restic snapshot created", "app", app.Settings.Name, "snapshot", summary.SnapshotID)
	}

	// Advisory: the path is fixed, so the next run overwrites it.
	if err := os.Remove(stagingFile); err != nil {
		slog.Warn("Failed to delete staging file", "file", stagingFile, "error", err)
	}

	return info.Size(), nil
}

func (n *Namespace) checkRepo(ctx context.Context, stagingDir string, repo resticRepo) error {
	res, err := n.runResticRetryLock(ctx, stagingDir, repo, resticCheckArgs()...)
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
