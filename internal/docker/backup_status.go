package docker

import (
	"context"
	"fmt"
	"time"
)

// offsiteStaleAfter is when an offsite copy's age stops being a reassurance.
// Backups run daily (AutomaticTaskInterval), so this is two missed runs.
const offsiteStaleAfter = 48 * time.Hour

// BackupStatus is the install-wide answer to "are my backups safe?", gathered
// once and rendered per frontend: `once backup status` prints all of it, the
// dashboard prints the part that earns the space (ADR-0012 - rendering differs
// per frontend, the facts do not).
//
// It carries the four decided facts: offsite staleness (per app, from the
// snapshots query), the last check result per repo, the same-device warning,
// and configured-but-not-enabled with the probe error behind it.
type BackupStatus struct {
	Configured bool
	Enabled    bool
	LocalPath  string
	OffsiteURL string
	// LastProbe is the enable-time validation probe's result. It is what
	// explains a configuration that never got enabled.
	LastProbe *OperationResult
	// OnDataDevice is the same-device warning: the local Backup Repo shares a
	// device with the Docker root, so one disk failure takes both. Advisory
	// and persistent - never a refusal.
	OnDataDevice bool
	// Checks is the last restic check per repo, keyed RepoNameLocal and
	// RepoNameOffsite. A damaged repo is a different emergency from a backup
	// that did not run, so it is recorded and reported separately.
	Checks map[string]OperationResult
	Apps   []AppBackupStatus
}

// AppBackupStatus is one application's row: whether it participates at all,
// the local leg's last result, and the offsite staleness answer.
type AppBackupStatus struct {
	Name       string
	Included   bool
	LastBackup *OperationResult
	Offsite    OffsiteAge
}

// OffsiteAge is the staleness fact for one app, in its three states: not asked
// (no offsite repository configured), asked and unanswerable (Err - the repo
// is unreachable, which is not the same as "never backed up"), and answered
// (Latest, zero meaning the repo holds no copy of this app yet).
type OffsiteAge struct {
	Queried bool
	Latest  time.Time
	Err     error
}

// Unknown reports that the repo could not be asked. It never collapses into
// Missing: telling someone with a full repo that they have no backup is the
// one answer worse than saying nothing.
func (a OffsiteAge) Unknown() bool {
	return a.Queried && a.Err != nil
}

// Missing reports that the repo answered and holds no copy of this app.
func (a OffsiteAge) Missing() bool {
	return a.Queried && a.Err == nil && a.Latest.IsZero()
}

// Age is how old the offsite copy is; meaningless unless the repo answered
// with a snapshot.
func (a OffsiteAge) Age() time.Duration {
	return time.Since(a.Latest)
}

// Stale reports an offsite copy old enough to be a warning rather than a
// reassurance: backups run daily, so this is two missed runs.
func (a OffsiteAge) Stale() bool {
	return a.Age() >= offsiteStaleAfter
}

// AgeText words the duration once, for every surface. How a frontend frames
// the sentence is its own business (ADR-0012), but two surfaces disagreeing
// about how old the same copy is would just be a bug.
func (a OffsiteAge) AgeText() string {
	d := a.Age()
	switch {
	case d < time.Hour:
		return "less than an hour"
	case d < offsiteStaleAfter:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
}

// RepoCheck pairs a repository name with its last check result.
type RepoCheck struct {
	Name   string
	Result OperationResult
}

// BackupStatus gathers the facts. The offsite staleness comes from the repo
// itself, never from State: State is a cache that dies with the install, and
// the person reading this on a rebuilt host is exactly the one who must not be
// told "never backed up" about a repo full of snapshots.
func (n *Namespace) BackupStatus(ctx context.Context) (BackupStatus, error) {
	cfg := n.BackupRepoSettings()
	status := BackupStatus{
		Configured: cfg.Configured(),
		Enabled:    cfg.Enabled,
		LocalPath:  cfg.LocalPath,
		OffsiteURL: cfg.OffsiteURL,
	}
	if !status.Configured {
		return status, nil
	}
	status.OnDataDevice = n.BackupOnDataDevice(ctx)

	state, err := n.LoadState(ctx)
	if err != nil {
		return status, fmt.Errorf("loading state: %w", err)
	}
	status.LastProbe = state.LastProbeResult()
	status.Checks = state.LastCheck

	for _, app := range n.Applications() {
		row := AppBackupStatus{
			Name:     app.Settings.Name,
			Included: app.Settings.Backup.AutoBackup,
		}
		if row.Included {
			row.LastBackup = state.AppState(row.Name).LastBackupResult()
			if cfg.OffsiteURL != "" {
				row.Offsite.Queried = true
				row.Offsite.Latest, row.Offsite.Err = n.LatestOffsiteSnapshot(ctx, row.Name)
			}
		}
		status.Apps = append(status.Apps, row)
	}
	return status, nil
}

// OffsiteSummary collapses the per-app staleness into the one answer a compact
// surface has room for, and it has to be the worst one: a definite gap outranks
// a repo that could not be asked, which outranks the oldest copy that exists.
func (s BackupStatus) OffsiteSummary() OffsiteAge {
	var worst OffsiteAge
	for _, app := range s.Apps {
		if !app.Included || !app.Offsite.Queried {
			continue
		}
		switch {
		case app.Offsite.Missing():
			return app.Offsite
		case app.Offsite.Unknown():
			worst = app.Offsite
		case !worst.Unknown() && (!worst.Queried || app.Offsite.Latest.Before(worst.Latest)):
			worst = app.Offsite
		}
	}
	return worst
}

// RepoChecks lists the recorded check results in a fixed local-then-offsite
// order, so status output does not shuffle between runs.
func (s BackupStatus) RepoChecks() []RepoCheck {
	var checks []RepoCheck
	for _, name := range []string{RepoNameLocal, RepoNameOffsite} {
		if result, ok := s.Checks[name]; ok {
			checks = append(checks, RepoCheck{Name: name, Result: result})
		}
	}
	return checks
}

// FailedChecks is the repos whose last check found problems - the alarm that
// fires on its own, separately from a failed run.
func (s BackupStatus) FailedChecks() []RepoCheck {
	var failed []RepoCheck
	for _, check := range s.RepoChecks() {
		if check.Result.Error != "" {
			failed = append(failed, check)
		}
	}
	return failed
}
