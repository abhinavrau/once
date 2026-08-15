package docker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/basecamp/once/internal/system"
)

// backupProbeFile is the Staging Directory entry the validation probe backs
// up. Fixed forever: it is baked into every probe snapshot, and a varying
// path would mint a new retention group per attempt.
const backupProbeFile = ".once-probe"

// ConfigureBackups persists cfg with Enabled withheld, then validates every
// configured repo with the bounded probe and records the outcome in State.
// The password is owned here, not by the callers: a cfg without one takes
// the persisted password first - a retry must reuse the password the repos
// were initialized with, never regenerate it - and only mints a fresh one
// when none has ever existed. Callers read it back via BackupRepoSettings.
// On probe failure everything stays persisted, password included - the
// probe may already have initialized a repo with it, and discarding it
// would orphan that repo. The returned error carries restic's own message.
func (n *Namespace) ConfigureBackups(ctx context.Context, cfg BackupRepoSettings) error {
	if cfg.Password == "" {
		cfg.Password = n.BackupRepoSettings().Password
	}
	if cfg.Password == "" {
		password, err := GenerateRepoPassword()
		if err != nil {
			return fmt.Errorf("generating repository password: %w", err)
		}
		cfg.Password = password
	}

	cfg.Enabled = false
	if err := n.SetBackupRepoSettings(ctx, cfg); err != nil {
		return fmt.Errorf("saving backup settings: %w", err)
	}

	probeErr := n.ValidateBackupRepos(ctx, cfg)

	// The proxy label write above recreated the Proxy and with it the state
	// file, so this starts from whatever survived. Best effort: the probe
	// verdict is what matters, the record only feeds `once backup status`.
	if state, err := n.LoadState(ctx); err != nil {
		slog.Warn("Failed to load state after backup probe", "error", err)
	} else {
		state.RecordProbe(probeErr)
		if err := n.SaveState(ctx, state); err != nil {
			slog.Warn("Failed to record backup probe result", "error", err)
		}
	}

	return probeErr
}

// EnableBackups flips Enabled on an already-validated configuration.
func (n *Namespace) EnableBackups(ctx context.Context) error {
	cfg := n.BackupRepoSettings()
	cfg.Enabled = true
	return n.SetBackupRepoSettings(ctx, cfg)
}

// ValidateBackupRepos is the enable-time validation probe: synthetic and
// bounded, because it may run inline mid-install before any app exists. Per
// configured repo it ensures the repo (cat config / init), backs up a probe
// file with the real --host and a fixed path, and forgets the snapshot
// without --prune. No check, no prune - both are unbounded against a large
// pre-existing repo.
func (n *Namespace) ValidateBackupRepos(ctx context.Context, cfg BackupRepoSettings) error {
	if err := os.MkdirAll(cfg.StagingDir(), 0o700); err != nil {
		return fmt.Errorf("creating staging directory: %w", err)
	}
	if err := os.MkdirAll(cfg.LocalPath, 0o700); err != nil {
		return fmt.Errorf("creating repository directory: %w", err)
	}

	probePath := filepath.Join(cfg.StagingDir(), backupProbeFile)
	if err := os.WriteFile(probePath, []byte("once backup validation probe\n"), 0o600); err != nil {
		return fmt.Errorf("writing probe file: %w", err)
	}
	defer os.Remove(probePath)

	if err := n.probeRepo(ctx, "local", localResticRepo(cfg)); err != nil {
		return err
	}
	if cfg.OffsiteURL != "" {
		if err := n.probeRepo(ctx, "offsite", offsiteResticRepo(cfg)); err != nil {
			return err
		}
	}
	return nil
}

// BackupOnDataDevice reports whether the local Backup Repo shares a device
// with the Docker root - the disk the Application Data Volumes live on. A
// warn-only surface, so any error degrades to false.
func (n *Namespace) BackupOnDataDevice(ctx context.Context) bool {
	cfg := n.BackupRepoSettings()
	if cfg.LocalPath == "" {
		return false
	}
	dockerRoot, err := n.DockerRootDir(ctx)
	if err != nil {
		return false
	}
	same, err := system.SameDevice(dockerRoot, cfg.LocalPath)
	return err == nil && same
}

// Private

func (n *Namespace) probeRepo(ctx context.Context, name string, repo resticRepo) error {
	if err := n.ensureRepo(ctx, repo); err != nil {
		return fmt.Errorf("%s repository: %w", name, err)
	}

	res, err := n.runResticRetryLock(ctx, repo, resticProbeBackupArgs(n.name)...)
	if err != nil {
		return err
	}
	if res.exitCode != 0 {
		return fmt.Errorf("%s repository: restic backup failed: %s", name, res.failure())
	}

	summary, ok := parseResticSummary(res.stdout)
	if !ok || summary.SnapshotID == "" {
		return fmt.Errorf("%s repository: restic backup reported no snapshot", name)
	}

	// forget by id, never --prune: the probe must leave no snapshot behind,
	// and prune is unbounded against a pre-existing repo.
	forgetRes, err := n.runResticRetryLock(ctx, repo, "forget", summary.SnapshotID)
	if err != nil {
		return err
	}
	if forgetRes.exitCode != 0 {
		return fmt.Errorf("%s repository: restic forget failed: %s", name, forgetRes.failure())
	}
	return nil
}
