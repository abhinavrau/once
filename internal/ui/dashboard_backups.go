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

// backupStatusRefresh is how long after one gather returns the next one
// starts. The staleness answer costs a restic container per participating app
// and the facts behind it move on a daily cadence, so a frame-rate refresh
// would spend real work to show a number that cannot have changed.
const backupStatusRefresh = 5 * time.Minute

// backupStatusTimeout bounds one gather. restic retries an unreachable backend
// with exponential backoff for about fifteen minutes (issues/15), so without a
// deadline a dead uplink would leave a gather running long enough to overlap
// the next one, stacking restic containers for as long as it stayed down.
const backupStatusTimeout = 2 * time.Minute

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

func (m Dashboard) backupNote() string {
	return renderBackupNote(m.backupStatus, m.width)
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
		ctx, cancel := context.WithTimeout(context.Background(), backupStatusTimeout)
		defer cancel()
		status, _ := ns.BackupStatus(ctx)
		return backupStatusMsg{status: status}
	}
}

// scheduleBackupStatusRefresh arms the next gather. It is armed by the arrival
// of a result, never on a fixed interval, so there is at most one gather in
// flight however slow a repository is.
func (m Dashboard) scheduleBackupStatusRefresh() tea.Cmd {
	return tea.Tick(backupStatusRefresh, func(time.Time) tea.Msg { return backupStatusRefreshMsg{} })
}

// Helpers

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
			text:  "⚠ Backups are configured, not enabled - run `once backup enable`",
			color: Colors.Warning,
		}
	}
	// Whether an offsite repository exists is a setting, not something the
	// snapshots query can answer: no participating app, or a State that failed
	// to load, would otherwise read as "no offsite copy configured".
	if s.OffsiteURL == "" {
		return backupNoteLine{text: "Backups: enabled, no offsite copy configured", color: Colors.Muted}
	}

	age := s.OffsiteSummary()
	switch {
	case !age.Queried:
		return backupNoteLine{text: "Backups: enabled, no app is included yet", color: Colors.Muted}
	case age.Unknown():
		// Transient by nature - the uplink is down, not the backup. It never
		// becomes "no offsite copy": that would tell someone with a full repo
		// they have nothing.
		return backupNoteLine{text: "Backups: offsite copy age unknown", color: Colors.Muted}
	case age.Missing():
		return backupNoteLine{text: "⚠ Backups: no offsite copy yet", color: Colors.Warning}
	case age.Stale():
		return backupNoteLine{text: "⚠ Backups: offsite copy is " + age.AgeText() + " old", color: Colors.Warning}
	default:
		return backupNoteLine{text: "Backups: offsite copy is " + age.AgeText() + " old", color: Colors.Muted}
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
