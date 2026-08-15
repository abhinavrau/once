package docker

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOffsiteAgeStates(t *testing.T) {
	none := OffsiteAge{}
	assert.False(t, none.Queried)
	assert.False(t, none.Unknown())
	assert.False(t, none.Missing())

	unknown := OffsiteAge{Queried: true, Err: errors.New("unreachable")}
	assert.True(t, unknown.Unknown())
	assert.False(t, unknown.Missing(), "a repo that could not be asked never means no copy exists")

	missing := OffsiteAge{Queried: true}
	assert.True(t, missing.Missing())
	assert.False(t, missing.Unknown())

	fresh := OffsiteAge{Queried: true, Latest: time.Now().Add(-90 * time.Minute)}
	assert.False(t, fresh.Missing())
	assert.False(t, fresh.Unknown())
	assert.InDelta(t, 90*time.Minute, fresh.Age(), float64(time.Minute))
}

// The duration is worded once, in the facts, so `once backup status` and the
// dashboard can never disagree about how old the same copy is.
func TestOffsiteAgeText(t *testing.T) {
	age := func(d time.Duration) OffsiteAge {
		return OffsiteAge{Queried: true, Latest: time.Now().Add(-d)}
	}

	assert.Equal(t, "less than an hour", age(time.Minute).AgeText())
	assert.Equal(t, "6 hours", age(6*time.Hour).AgeText())
	assert.Equal(t, "3 days", age(73*time.Hour).AgeText())

	// Two missed daily runs is where reassurance turns into a warning.
	assert.False(t, age(47*time.Hour).Stale())
	assert.True(t, age(49*time.Hour).Stale())
}

// The dashboard has room for one staleness answer, so the install-wide summary
// has to be the worst one: a definite gap outranks an unreachable repo, which
// outranks the oldest copy that does exist.
func TestOffsiteSummaryTakesTheWorstApp(t *testing.T) {
	now := time.Now()
	fresh := AppBackupStatus{Name: "a", Included: true, Offsite: OffsiteAge{Queried: true, Latest: now.Add(-time.Hour)}}
	old := AppBackupStatus{Name: "b", Included: true, Offsite: OffsiteAge{Queried: true, Latest: now.Add(-72 * time.Hour)}}
	unknown := AppBackupStatus{Name: "c", Included: true, Offsite: OffsiteAge{Queried: true, Err: errors.New("unreachable")}}
	missing := AppBackupStatus{Name: "d", Included: true, Offsite: OffsiteAge{Queried: true}}
	excluded := AppBackupStatus{Name: "e", Offsite: OffsiteAge{Queried: true}}

	assert.Equal(t, old.Offsite, BackupStatus{Apps: []AppBackupStatus{fresh, old}}.OffsiteSummary())
	assert.True(t, BackupStatus{Apps: []AppBackupStatus{fresh, old, unknown}}.OffsiteSummary().Unknown())
	assert.True(t, BackupStatus{Apps: []AppBackupStatus{fresh, unknown, missing}}.OffsiteSummary().Missing())

	// An app that opted out of backups says nothing about the offsite copy.
	assert.False(t, BackupStatus{Apps: []AppBackupStatus{fresh, excluded}}.OffsiteSummary().Missing())
	assert.False(t, BackupStatus{Apps: []AppBackupStatus{excluded}}.OffsiteSummary().Queried)
}

func TestChecksAreOrderedLocalThenOffsite(t *testing.T) {
	at := time.Date(2026, 8, 14, 3, 4, 0, 0, time.UTC)
	status := BackupStatus{Checks: map[string]OperationResult{
		RepoNameOffsite: {At: at, Error: "restic check found problems: 2 errors"},
		RepoNameLocal:   {At: at},
	}}

	checks := status.RepoChecks()
	require.Len(t, checks, 2)
	assert.Equal(t, RepoNameLocal, checks[0].Name)
	assert.Equal(t, RepoNameOffsite, checks[1].Name)

	// "Your repo is damaged" alarms on its own, separately from a failed run.
	failed := status.FailedChecks()
	require.Len(t, failed, 1)
	assert.Equal(t, RepoNameOffsite, failed[0].Name)
	assert.Empty(t, BackupStatus{Checks: map[string]OperationResult{RepoNameLocal: {At: at}}}.FailedChecks())
}
