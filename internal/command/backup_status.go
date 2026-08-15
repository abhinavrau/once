package command

import (
	"fmt"
	"strings"
	"time"

	"github.com/basecamp/once/internal/docker"
)

// The status table's fixed columns. The label and repository columns are
// constants rather than measured, so the same install renders identically
// whichever repos happen to have results yet; the application column is
// measured, because app names are the user's own and truncating them would
// hide which app the row is about.
const (
	statusLabelWidth = 19 // "Offsite repository:"
	statusRepoWidth  = 7  // "offsite"
	statusIndent     = "  "
)

// renderBackupStatus is the CLI's full rendering of the four facts: offsite
// staleness per app, the last check per repo, the same-device warning, and
// configured-but-not-enabled with the probe error behind it. Rendering differs
// per frontend, the facts do not (ADR-0012), so this takes the gathered facts
// and returns text - nothing here queries anything.
func renderBackupStatus(s docker.BackupStatus) string {
	if !s.Configured {
		return "Backups are not configured. Run `once backup enable` to set them up.\n"
	}

	var b strings.Builder
	writeBackupSummary(&b, s)
	writeBackupApps(&b, s)
	writeBackupChecks(&b, s)
	return b.String()
}

// Private

func writeBackupSummary(b *strings.Builder, s docker.BackupStatus) {
	if s.Enabled {
		fmt.Fprintln(b, "Backups: enabled")
	} else {
		fmt.Fprintln(b, "Backups: configured, not enabled")
	}

	writeLabelled(b, "Local repository:", s.LocalPath)
	if s.OffsiteURL != "" {
		writeLabelled(b, "Offsite repository:", s.OffsiteURL)
	}
	if !s.Enabled {
		if s.LastProbe != nil {
			writeLabelled(b, "Last validation:", describeOutcome(*s.LastProbe))
			if s.LastProbe.Error != "" {
				fmt.Fprintln(b, statusIndent+strings.Repeat(" ", statusLabelWidth+1)+s.LastProbe.Error)
			}
		}
		fmt.Fprintln(b, statusIndent+"Run `once backup enable` to finish enabling backups.")
	}

	// Persistent, never a refusal: the install works, it is just one disk
	// failure away from losing the data and its backup together.
	if s.OnDataDevice {
		fmt.Fprintln(b)
		fmt.Fprintln(b, statusIndent+"Warning: the local repository is on the same disk as your application data.")
		if s.OffsiteURL != "" {
			fmt.Fprintln(b, statusIndent+"If that disk fails you lose both; the offsite copy is your only protection.")
		} else {
			fmt.Fprintln(b, statusIndent+"If that disk fails you lose both; set an offsite repository.")
		}
	}
}

func writeBackupApps(b *strings.Builder, s docker.BackupStatus) {
	if len(s.Apps) == 0 {
		return
	}

	width := 0
	for _, app := range s.Apps {
		width = max(width, len(app.Name))
	}
	detail := statusIndent + strings.Repeat(" ", width+2)

	fmt.Fprintln(b, "\nApplications")
	for _, app := range s.Apps {
		fmt.Fprintf(b, "%s%-*s  %s\n", statusIndent, width, app.Name, describeAppBackup(app))
		if app.LastBackup != nil && app.LastBackup.Error != "" {
			fmt.Fprintln(b, detail+app.LastBackup.Error)
		}
		// The staleness query's own failure is the evidence behind "unknown",
		// and it is never folded into the run's outcome.
		if app.Offsite.Unknown() {
			fmt.Fprintln(b, detail+"offsite: "+docker.ErrorMessage(app.Offsite.Err))
		}
	}
}

func writeBackupChecks(b *strings.Builder, s docker.BackupStatus) {
	checks := s.RepoChecks()
	if len(checks) == 0 {
		return
	}

	fmt.Fprintln(b, "\nRepository checks")
	for _, check := range checks {
		fmt.Fprintf(b, "%s%-*s  %s\n", statusIndent, statusRepoWidth, check.Name, describeCheckResult(check.Result))
	}
	if len(s.FailedChecks()) > 0 {
		fmt.Fprintln(b, "\nA repository that fails its check may not restore. This is a separate")
		fmt.Fprintln(b, "emergency from a backup that did not run.")
	}
}

func writeLabelled(b *strings.Builder, label, value string) {
	fmt.Fprintf(b, "%s%-*s %s\n", statusIndent, statusLabelWidth, label, value)
}

// Helpers

// describeAppBackup is one application's row: the local leg's last result and,
// when an offsite repository is configured, the staleness answer beside it.
func describeAppBackup(app docker.AppBackupStatus) string {
	if !app.Included {
		return "not included in backups"
	}
	line := describeLastBackup(app.LastBackup)
	if age := describeOffsiteAge(app.Offsite); age != "" {
		line += ", " + age
	}
	return line
}

func describeLastBackup(r *docker.OperationResult) string {
	if r == nil {
		return "never backed up"
	}
	return "last backup " + describeOutcome(*r)
}

// describeCheckResult words a check apart from a run: "your repo is damaged"
// is a different emergency from "your backup did not run", and sharing the
// word "failed" for both is exactly how that distinction gets lost.
func describeCheckResult(r docker.OperationResult) string {
	if r.Error != "" {
		return fmt.Sprintf("PROBLEM %s: %s", formatOperationTime(r), r.Error)
	}
	return "ok " + formatOperationTime(r)
}

// describeOutcome is the verdict without the reason: reasons are unbounded
// restic output, and inlining one turns every aligned table into ragged wrap.
// Callers put it on its own continuation line instead.
func describeOutcome(r docker.OperationResult) string {
	if r.Error != "" {
		return "failed " + formatOperationTime(r)
	}
	return "ok " + formatOperationTime(r)
}

// describeOffsiteAge renders the staleness signal from the offsite snapshots
// query - the raw fact, so a flaky uplink shows up somewhere even though it
// never fails a run. An unreachable repo is "unknown", never "no copy": the
// person reading this on a rebuilt host is the one who must not be told their
// full repo is empty.
func describeOffsiteAge(age docker.OffsiteAge) string {
	switch {
	case !age.Queried:
		return ""
	case age.Unknown():
		return "offsite copy age unknown"
	case age.Missing():
		return "no offsite copy yet"
	default:
		return "offsite copy is " + describeAge(age.Age()) + " old"
	}
}

func describeAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		return "less than an hour"
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
}

func formatOperationTime(r docker.OperationResult) string {
	return r.At.Format("2006-01-02 15:04")
}
