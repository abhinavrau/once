package docker

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/basecamp/once/internal/system"
)

// RestoreRepo names a Backup Repo to restore from. Deliberately not
// BackupRepoSettings: bare-metal recovery starts with no configuration at all
// - a URL, a password, and whatever the environment holds - so nothing in the
// restore flow may assume a local repo, or any settings, exist.
type RestoreRepo struct {
	// URL is a restic backend URL or an absolute path.
	URL      string
	Password string
	// Env carries backend credentials into the restic container by
	// environment, never argv (ADR-0013).
	Env map[string]string
	// StagingDir is where the archive is restored to before it is opened.
	StagingDir string
}

// RepoSnapshot is one app archive in a Backup Repo, as restore needs to see it
// before deciding anything. Host is the namespace of the install that wrote
// it, which on a bare-metal recovery is the machine that is gone.
type RepoSnapshot struct {
	ID    string
	Time  time.Time
	App   string
	Image string
	Host  string
	Path  string
	Bytes int64
}

// RepoApp is one application's presence in a repo: the discovery listing a
// user gets before they trust anything in it.
type RepoApp struct {
	Name      string
	Snapshots int
	Newest    time.Time
	Image     string
}

// RepoSnapshots is a repo's snapshots, oldest first.
type RepoSnapshots []RepoSnapshot

// Apps summarizes the repo one line per application, newest image first,
// ordered by name.
func (s RepoSnapshots) Apps() []RepoApp {
	byName := map[string]*RepoApp{}
	for _, snapshot := range s {
		app, ok := byName[snapshot.App]
		if !ok {
			app = &RepoApp{Name: snapshot.App}
			byName[snapshot.App] = app
		}
		app.Snapshots++
		if !snapshot.Time.Before(app.Newest) {
			app.Newest = snapshot.Time
			app.Image = snapshot.Image
		}
	}

	apps := make([]RepoApp, 0, len(byName))
	for _, app := range byName {
		apps = append(apps, *app)
	}
	slices.SortFunc(apps, func(a, b RepoApp) int { return cmp.Compare(a.Name, b.Name) })
	return apps
}

// ForApp narrows the repo to one application's history.
func (s RepoSnapshots) ForApp(name string) RepoSnapshots {
	var history RepoSnapshots
	for _, snapshot := range s {
		if snapshot.App == name {
			history = append(history, snapshot)
		}
	}
	return history
}

// Latest is the default pick: the newest snapshot of an app.
func (s RepoSnapshots) Latest(name string) (RepoSnapshot, bool) {
	history := s.ForApp(name)
	if len(history) == 0 {
		return RepoSnapshot{}, false
	}
	return history[len(history)-1], true
}

// Find picks an older snapshot by the short id the history listing prints.
func (s RepoSnapshots) Find(name, id string) (RepoSnapshot, bool) {
	for _, snapshot := range s.ForApp(name) {
		if snapshot.ID == id {
			return snapshot, true
		}
	}
	return RepoSnapshot{}, false
}

// ListRepoSnapshots reads what a Backup Repo holds. It filters by neither host
// nor tag: the snapshots a bare-metal recovery is looking for were written by
// an install that no longer exists and may not have shared this one's
// namespace, so scoping the question to this host would answer "empty" to the
// person who needs it most.
func (n *Namespace) ListRepoSnapshots(ctx context.Context, repo RestoreRepo) (RepoSnapshots, error) {
	if err := checkRepoReachable(repo.URL); err != nil {
		return nil, err
	}

	// A question needs no Staging Directory, and a fresh host has none.
	repo.StagingDir = ""
	res, err := n.runRestic(ctx, restoreResticRepo(repo), resticRepoSnapshotsArgs()...)
	if err != nil {
		return nil, err
	}

	switch res.exitCode {
	case 0:
		return parseRepoSnapshots(res.stdout)
	case resticExitNoRepository:
		return nil, fmt.Errorf("no backup repository at %s", repo.URL)
	case resticExitWrongPassword:
		return nil, fmt.Errorf("wrong password for backup repository %s", repo.URL)
	default:
		return nil, fmt.Errorf("restic snapshots failed: %s", res.failure())
	}
}

// RestoreFromSnapshot restores one snapshot's Staging File into the Staging
// Directory and hands it to Restore, which creates a brand new application
// from the archive's own settings. Restic restores the archive to a file
// rather than streaming it: `dump` would push a multi-GB tar through container
// stdout plumbing Once does not have.
//
// It takes the same host flock a Backup Run takes, and applies the same
// free-disk rule against the size the snapshot itself records.
func (n *Namespace) RestoreFromSnapshot(ctx context.Context, repo RestoreRepo, snapshot RepoSnapshot) (*Application, error) {
	if err := checkRepoReachable(repo.URL); err != nil {
		return nil, err
	}

	lock, err := system.TryLock(BackupLockPath)
	if errors.Is(err, system.ErrLocked) {
		return nil, ErrBackupInProgress
	}
	if err != nil {
		return nil, fmt.Errorf("taking backup lock: %w", err)
	}
	defer lock.Unlock()

	if err := os.MkdirAll(repo.StagingDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", repo.StagingDir, err)
	}
	free, err := system.FreeDisk(repo.StagingDir)
	if err != nil {
		return nil, fmt.Errorf("checking free disk: %w", err)
	}
	if err := backupDiskCheck(free, snapshot.Bytes); err != nil {
		return nil, err
	}

	staged := filepath.Join(repo.StagingDir, filepath.Base(snapshot.Path))
	defer func() {
		if err := os.Remove(staged); err != nil && !os.IsNotExist(err) {
			slog.Warn("Failed to delete the staged archive", "file", staged, "error", err)
		}
	}()

	res, err := n.runResticRetryLock(ctx, restoreResticRepo(repo), resticRestoreArgs(snapshot.ID)...)
	if err != nil {
		return nil, err
	}
	if res.exitCode != 0 {
		return nil, fmt.Errorf("restic restore failed: %s", res.failure())
	}

	file, err := os.Open(staged)
	if err != nil {
		return nil, fmt.Errorf("opening the restored archive: %w", err)
	}
	defer file.Close()

	return n.Restore(ctx, file)
}

// ResumeBackupsAfterRestore configures backups on an install that has just
// recovered from a repo: that repo becomes the Offsite Repo and a fresh local
// one is initialized beside it. Recovering and then silently not backing up is
// the trap this closes.
//
// The local repo is initialized with --copy-chunker-params from the remote -
// the mirror image of what an offsite init does when the local repo is the one
// that exists - so that `restic copy` stays a one-shot backfill between them
// rather than a full re-chunk. Everything else is the ordinary enable path:
// the caller still runs the probe's verdict, the custody moment, and Enabled.
func (n *Namespace) ResumeBackupsAfterRestore(ctx context.Context, cfg BackupRepoSettings) error {
	if err := prepareBackupDirs(cfg); err != nil {
		return err
	}
	if cfg.OffsiteURL != "" {
		if err := n.ensureRepo(ctx, localResticRepoFrom(cfg)); err != nil {
			return fmt.Errorf("initializing a local repository from %s: %w", cfg.OffsiteURL, err)
		}
	}
	return n.ConfigureBackups(ctx, cfg)
}

// Helpers

// checkRepoReachable answers the one question Docker would otherwise answer
// badly: a repository that is a filesystem path has to exist before it can be
// bind-mounted, and a missing bind source comes back as a daemon error naming
// an in-container path the user never typed.
func checkRepoReachable(url string) error {
	dir := offsiteDirFromURL(url)
	if dir == "" {
		return nil
	}
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("no backup repository at %s", url)
	}
	return nil
}

// restoreResticRepo addresses the repo a restore reads. A filesystem path is
// bind-mounted and addressed by its in-container path, exactly as an offsite
// path repo is; anything else restic reaches over the network with the backend
// credentials in the environment. The Staging Directory is mounted read-write:
// here restic is the writer.
func restoreResticRepo(r RestoreRepo) resticRepo {
	repo := resticRepo{
		repository:      r.URL,
		displayName:     r.URL,
		password:        r.Password,
		env:             r.Env,
		stagingDir:      r.StagingDir,
		stagingWritable: true,
	}
	if dir := offsiteDirFromURL(r.URL); dir != "" {
		repo.localRepoDir = dir
		repo.repository = resticRepoMount
	}
	return repo
}

// localResticRepoFrom addresses the local Backup Repo with the Offsite Repo
// alongside it as the RESTIC_FROM_* source, so init can copy the remote's
// chunker parameters into a local repo that does not exist yet.
func localResticRepoFrom(cfg BackupRepoSettings) resticRepo {
	offsite := offsiteResticRepo(cfg)

	env := make(map[string]string, len(cfg.OffsiteEnv)+2)
	maps.Copy(env, cfg.OffsiteEnv)
	env["RESTIC_FROM_REPOSITORY"] = offsite.repository
	env["RESTIC_FROM_PASSWORD"] = cfg.Password

	repo := localResticRepo(cfg)
	repo.env = env
	repo.offsiteRepoDir = offsite.offsiteRepoDir
	repo.initArgs = []string{"init", "--copy-chunker-params"}
	return repo
}

// parseRepoSnapshots reads `restic snapshots --json`. Snapshots with no app
// tag or no path are skipped rather than guessed at: a probe snapshot left by
// a SIGKILLed enable, or another tool's snapshot in a shared repo, is not
// something Once can restore.
func parseRepoSnapshots(output string) (RepoSnapshots, error) {
	var raw []struct {
		ShortID  string    `json:"short_id"`
		Time     time.Time `json:"time"`
		Hostname string    `json:"hostname"`
		Tags     []string  `json:"tags"`
		Paths    []string  `json:"paths"`
		Summary  struct {
			TotalBytesProcessed int64 `json:"total_bytes_processed"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &raw); err != nil {
		return nil, fmt.Errorf("parsing restic snapshots: %w", err)
	}

	var snapshots RepoSnapshots
	for _, entry := range raw {
		snapshot := RepoSnapshot{
			ID:    entry.ShortID,
			Time:  entry.Time,
			Host:  entry.Hostname,
			Bytes: entry.Summary.TotalBytesProcessed,
		}
		for _, tag := range entry.Tags {
			if app, ok := strings.CutPrefix(tag, snapshotAppTag); ok {
				snapshot.App = app
			}
			if image, ok := strings.CutPrefix(tag, snapshotImageTag); ok {
				snapshot.Image = image
			}
		}
		if len(entry.Paths) > 0 {
			snapshot.Path = entry.Paths[0]
		}
		if snapshot.App == "" || snapshot.Path == "" {
			continue
		}
		snapshots = append(snapshots, snapshot)
	}

	slices.SortFunc(snapshots, func(a, b RepoSnapshot) int {
		return cmp.Or(a.Time.Compare(b.Time), cmp.Compare(a.ID, b.ID))
	})
	return snapshots, nil
}
