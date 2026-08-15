package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
)

func enabledBackupStatus(apps ...docker.AppBackupStatus) docker.BackupStatus {
	return docker.BackupStatus{
		Configured: true,
		Enabled:    true,
		LocalPath:  "/var/lib/once/backups",
		OffsiteURL: "s3:s3.amazonaws.com/bucket/once",
		Apps:       apps,
	}
}

func includedApp(age docker.OffsiteAge) docker.AppBackupStatus {
	return docker.AppBackupStatus{Name: "vaultwarden", Included: true, Offsite: age}
}

func noteTexts(s docker.BackupStatus) []string {
	var texts []string
	for _, line := range backupNoteLines(s) {
		texts = append(texts, line.text)
	}
	return texts
}

// An install with no Backup Repo has nothing to report and must not spend a
// dashboard row saying so.
func TestBackupNoteSaysNothingWhenUnconfigured(t *testing.T) {
	assert.Empty(t, backupNoteLines(docker.BackupStatus{}))
	assert.Empty(t, renderBackupNote(docker.BackupStatus{}, 80))
}

func TestBackupNoteStaleness(t *testing.T) {
	fresh := noteTexts(enabledBackupStatus(includedApp(docker.OffsiteAge{Queried: true, Latest: time.Now().Add(-6 * time.Hour)})))
	assert.Equal(t, []string{"Backups: offsite copy is 6 hours old"}, fresh)

	stale := noteTexts(enabledBackupStatus(includedApp(docker.OffsiteAge{Queried: true, Latest: time.Now().Add(-73 * time.Hour)})))
	assert.Equal(t, []string{"⚠ Backups: offsite copy is 3 days old"}, stale)

	missing := noteTexts(enabledBackupStatus(includedApp(docker.OffsiteAge{Queried: true})))
	assert.Equal(t, []string{"⚠ Backups: no offsite copy yet"}, missing)

	// No offsite repository at all is a 3-2-1 gap, but not an alarm.
	local := enabledBackupStatus(includedApp(docker.OffsiteAge{}))
	local.OffsiteURL = ""
	assert.Equal(t, []string{"Backups: enabled, no offsite copy configured"}, noteTexts(local))
}

// Whether an offsite repository exists is a setting, not something the
// snapshots query can answer: an install with one configured but no app
// participating yet must not read as though none were configured.
func TestBackupNoteOffsiteConfiguredComesFromTheSetting(t *testing.T) {
	noApps := enabledBackupStatus()
	assert.Equal(t, []string{"Backups: enabled, no app is included yet"}, noteTexts(noApps))

	excluded := enabledBackupStatus(docker.AppBackupStatus{Name: "writebook"})
	assert.Equal(t, []string{"Backups: enabled, no app is included yet"}, noteTexts(excluded))
}

// An unreachable repo is unknown, never "no offsite copy": a flaky uplink must
// not tell the user their offsite repository is empty.
func TestBackupNoteUnknownStalenessIsNotMissing(t *testing.T) {
	unknown := noteTexts(enabledBackupStatus(includedApp(docker.OffsiteAge{Queried: true, Err: errors.New("dial tcp: timeout")})))

	require.Len(t, unknown, 1)
	assert.Equal(t, "Backups: offsite copy age unknown", unknown[0])
	assert.NotContains(t, unknown[0], "⚠", "an unreachable repo is transient, not an alarm")
}

// A damaged repo alarms on its own: it is a different emergency from a backup
// that did not run, and it outranks staleness for the top line.
func TestBackupNoteCheckAlarm(t *testing.T) {
	status := enabledBackupStatus(includedApp(docker.OffsiteAge{Queried: true, Latest: time.Now()}))
	status.Checks = map[string]docker.OperationResult{
		docker.RepoNameOffsite: {At: time.Now(), Error: "restic check found problems: 2 errors"},
		docker.RepoNameLocal:   {At: time.Now()},
	}

	lines := backupNoteLines(status)
	require.NotEmpty(t, lines)
	assert.Equal(t, "⚠ The offsite backup repository failed its check - it may not restore", lines[0].text)
	assert.Equal(t, Colors.Error, lines[0].color)
	assert.Len(t, lines, 2, "a healthy local repo does not get an alarm of its own")
}

func TestBackupNoteSameDeviceWarningIsPersistent(t *testing.T) {
	status := enabledBackupStatus(includedApp(docker.OffsiteAge{Queried: true, Latest: time.Now()}))
	status.OnDataDevice = true

	assert.Contains(t, noteTexts(status), "⚠ Backups share a disk with your app data - keep an offsite copy")
}

func TestBackupNoteConfiguredButNotEnabled(t *testing.T) {
	status := enabledBackupStatus(includedApp(docker.OffsiteAge{Queried: true, Latest: time.Now()}))
	status.Enabled = false

	// Worded as the CLI words it: two surfaces disagreeing about the same
	// state is just a bug.
	assert.Equal(t, []string{"⚠ Backups are configured, not enabled - run `once backup enable`"}, noteTexts(status))
}

// Every line has to survive the dashboard's narrowest supported terminal
// without wrapping, which would silently steal a row from the app panels.
func TestBackupNoteFitsEightyColumns(t *testing.T) {
	status := enabledBackupStatus(includedApp(docker.OffsiteAge{Queried: true}))
	status.OnDataDevice = true
	status.Checks = map[string]docker.OperationResult{
		docker.RepoNameLocal:   {At: time.Now(), Error: "restic check found problems: 2 errors (repair index: true)"},
		docker.RepoNameOffsite: {At: time.Now(), Error: "restic check failed with exit code 1"},
	}

	for _, line := range backupNoteLines(status) {
		assert.LessOrEqual(t, lipgloss.Width(line.text), 80, line.text)
	}

	note := renderBackupNote(status, 80)
	for line := range strings.Lines(ansi.Strip(note)) {
		assert.Equal(t, 80, lipgloss.Width(strings.TrimRight(line, "\n")), "each note row is padded to exactly the terminal width")
	}
	assert.Equal(t, 4, lipgloss.Height(note), "both check alarms, the same-device warning, and the staleness line")
}

// A line longer than the terminal is truncated rather than wrapped, because a
// wrap costs a panel row that the viewport already accounted for.
func TestBackupNoteTruncatesRatherThanWraps(t *testing.T) {
	status := enabledBackupStatus(includedApp(docker.OffsiteAge{Queried: true, Latest: time.Now()}))

	note := renderBackupNote(status, 20)
	assert.Equal(t, 1, lipgloss.Height(note))
	assert.Contains(t, ansi.Strip(note), "…")
}

// The gather runs a restic container per participating app and an unreachable
// backend can keep one alive for minutes, so the next one is armed by the
// arrival of a result, never by a fixed interval - otherwise a dead uplink
// would stack gathers for as long as it stayed down.
func TestDashboardBackupRefreshIsArmedByTheResult(t *testing.T) {
	d := testDashboard(1)
	d, _ = updateDashboard(d, tea.WindowSizeMsg{Width: 120, Height: 40})

	// A refresh tick starts a gather and arms nothing itself, so the only
	// thing that can schedule the next one is a result coming back. With no
	// namespace there is nothing to gather, and the chain simply stops.
	_, refreshCmd := updateDashboard(d, backupStatusRefreshMsg{})
	assert.Nil(t, refreshCmd)

	_, resultCmd := updateDashboard(d, backupStatusMsg{status: enabledBackupStatus()})
	assert.NotNil(t, resultCmd, "the arriving result is what arms the next gather")
}

func TestDashboardBackupNoteShrinksTheViewport(t *testing.T) {
	plain := testDashboard(1)
	plain, _ = updateDashboard(plain, tea.WindowSizeMsg{Width: 120, Height: 40})

	warned := testDashboard(1)
	warned, _ = updateDashboard(warned, tea.WindowSizeMsg{Width: 120, Height: 40})
	warned, _ = updateDashboard(warned, backupStatusMsg{status: enabledBackupStatus(
		includedApp(docker.OffsiteAge{Queried: true, Latest: time.Now().Add(-6 * time.Hour)}),
	)})

	noteHeight := lipgloss.Height(warned.backupNote())
	assert.Equal(t, 1, noteHeight)
	assert.Equal(t, plain.viewport.Height()-noteHeight, warned.viewport.Height())
	assert.Equal(t, lipgloss.Height(plain.View()), lipgloss.Height(warned.View()))
	assert.Contains(t, ansi.Strip(warned.View()), "Backups: offsite copy is 6 hours old")
}
