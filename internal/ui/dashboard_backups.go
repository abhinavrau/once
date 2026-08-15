package ui

import (
	"context"
	"fmt"
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/basecamp/once/internal/docker"
)

// backupStatusRefresh is how often the dashboard re-asks. The staleness answer
// costs a restic container per participating app, and the facts behind it move
// on a daily cadence, so a frame-rate refresh would spend real work to show a
// number that cannot have changed.
const backupStatusRefresh = 5 * time.Minute

// offsiteStaleAfter is when an offsite copy's age becomes a warning rather
// than a reassurance: backups run daily, so this is two missed runs.
const offsiteStaleAfter = 48 * time.Hour

type backupStatusMsg struct {
	status docker.BackupStatus
}

type backupStatusRefreshMsg struct{}

// backupNoteLine is one compact line of the dashboard's backup note.
type backupNoteLine struct {
	text  string
	color color.Color
}

// Private

// backupNoteLines is the dashboard's share of the four facts (ADR-0012: the
// facts do not differ per frontend, the rendering does). `once backup status`
// prints all of them, per app; the dashboard has room for the install-wide
// ones that change what the user should do right now: a repository that failed
// its check, the same-device warning, and one staleness answer. Per-app
// last-run detail stays in Settings > Backups.
func backupNoteLines(s docker.BackupStatus) []backupNoteLine {
	if !s.Configured {
		return nil
	}

	var lines []backupNoteLine
	// The alarm leads: "your repo is damaged" is a different emergency from
	// "your backup did not run", and it is the one that is silent otherwise.
	for _, check := range s.FailedChecks() {
		lines = append(lines, backupNoteLine{
			text:  fmt.Sprintf("⚠ The %s backup repository failed its check - it may not restore", check.Name),
			color: Colors.Error,
		})
	}
	if s.OnDataDevice {
		lines = append(lines, backupNoteLine{
			text:  "⚠ Backups share a disk with your app data - keep an offsite copy",
			color: Colors.Warning,
		})
	}
	return append(lines, backupHeadlineLine(s))
}

// backupHeadlineLine is the one line the dashboard always spends on a
// configured install: what state backups are in, and how old the offsite copy
// is. A configuration that never got enabled outranks staleness, because a
// stale copy still beats one nothing is writing to.
func backupHeadlineLine(s docker.BackupStatus) backupNoteLine {
	if !s.Enabled {
		return backupNoteLine{
			text:  "⚠ Backups are configured but not enabled - run `once backup enable`",
			color: Colors.Warning,
		}
	}

	age := s.OffsiteSummary()
	switch {
	case !age.Queried:
		return backupNoteLine{text: "Backups: enabled, no offsite copy configured", color: Colors.Muted}
	case age.Unknown():
		// Transient by nature - the uplink is down, not the backup. It never
		// becomes "no offsite copy": that would tell someone with a full repo
		// they have nothing.
		return backupNoteLine{text: "Backups: offsite copy age unknown", color: Colors.Muted}
	case age.Missing():
		return backupNoteLine{text: "⚠ Backups: no offsite copy yet", color: Colors.Warning}
	case age.Age() >= offsiteStaleAfter:
		return backupNoteLine{text: "⚠ Backups: offsite copy is " + describeOffsiteAgeText(age) + " old", color: Colors.Warning}
	default:
		return backupNoteLine{text: "Backups: offsite copy is " + describeOffsiteAgeText(age) + " old", color: Colors.Muted}
	}
}

// renderBackupNote centers each line on its own row, truncating rather than
// wrapping: the viewport has already given up exactly this many rows, and a
// wrapped line would take one more from the app panels.
func renderBackupNote(s docker.BackupStatus, width int) string {
	lines := backupNoteLines(s)
	if len(lines) == 0 || width <= 0 {
		return ""
	}

	rendered := make([]string, len(lines))
	for i, line := range lines {
		text := ansi.Truncate(line.text, width, "…")
		rendered[i] = lipgloss.PlaceHorizontal(width, lipgloss.Center, lipgloss.NewStyle().Foreground(line.color).Render(text))
	}
	return strings.Join(rendered, "\n")
}

// describeOffsiteAgeText words a duration the way the CLI does, so the two
// surfaces never disagree about how old the same copy is.
func describeOffsiteAgeText(age docker.OffsiteAge) string {
	d := age.Age()
	switch {
	case d < time.Hour:
		return "less than an hour"
	case d < offsiteStaleAfter:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
}

// fetchBackupStatus asks off the UI goroutine: the staleness query runs a
// restic container per app, which must never cost a frame. A failure degrades
// to the facts gathered so far rather than blanking the note.
func (m Dashboard) fetchBackupStatus() tea.Cmd {
	ns := m.namespace
	if ns == nil {
		return nil
	}
	return func() tea.Msg {
		status, _ := ns.BackupStatus(context.Background())
		return backupStatusMsg{status: status}
	}
}

func (m Dashboard) scheduleBackupStatusRefresh() tea.Cmd {
	return tea.Every(backupStatusRefresh, func(time.Time) tea.Msg { return backupStatusRefreshMsg{} })
}

func (m Dashboard) backupNote() string {
	return renderBackupNote(m.backupStatus, m.width)
}
