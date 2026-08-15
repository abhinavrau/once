package docker

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// restic emits these about ten times a second while a backup runs; the last
// summary line is still what decides the outcome, everywhere.
const resticStatusLine = `{"message_type":"status","seconds_elapsed":1,"percent_done":0.42,"total_files":1,"files_done":0,"total_bytes":1024,"bytes_done":430}`

func TestResticBackupProgressReportsPercentages(t *testing.T) {
	var reports []DeployProgress
	onLine := resticBackupProgress(func(p DeployProgress) { reports = append(reports, p) }, "offsite")
	require.NotNil(t, onLine)

	onLine(resticStatusLine)
	onLine(`{"message_type":"summary","snapshot_id":"abc","total_bytes_processed":1024}`)
	onLine("not json at all")
	onLine(`{"message_type":"status","percent_done":1}`)

	require.Len(t, reports, 2, "only status lines move the bar")
	assert.Equal(t, DeployProgress{Stage: DeployStageUploading, Percentage: 42, Status: "uploading offsite"}, reports[0])
	assert.Equal(t, 100, reports[1].Percentage)
}

// nil is the legitimate headless adapter (ADR-0012), and it must cost nothing
// rather than being papered over with a no-op hook.
func TestResticBackupProgressIsNilWithoutACallback(t *testing.T) {
	assert.Nil(t, resticBackupProgress(nil, "offsite"))
}

func TestParseResticStatus(t *testing.T) {
	status, ok := parseResticStatus(resticStatusLine)
	require.True(t, ok)
	assert.InDelta(t, 0.42, status.PercentDone, 0.001)

	_, ok = parseResticStatus(`{"message_type":"summary","snapshot_id":"abc"}`)
	assert.False(t, ok)

	_, ok = parseResticStatus("")
	assert.False(t, ok)
}

// The log stream arrives in whatever chunks the daemon sends, which never
// line up with restic's lines.
func TestLineWriterSplitsAcrossChunkBoundaries(t *testing.T) {
	var lines []string
	w := &lineWriter{fn: func(line string) { lines = append(lines, line) }}

	for _, chunk := range []string{"first li", "ne\nsecond line\nthi", "rd line\n"} {
		n, err := w.Write([]byte(chunk))
		require.NoError(t, err)
		assert.Equal(t, len(chunk), n, "a tee writer must never short-write")
	}

	assert.Equal(t, []string{"first line", "second line", "third line"}, lines)
}

// A trailing line with no newline is restic's last word when the container
// dies mid-line; it is never worth a report, and never worth a panic.
func TestLineWriterIgnoresAnUnterminatedTail(t *testing.T) {
	var lines []string
	w := &lineWriter{fn: func(line string) { lines = append(lines, line) }}

	_, err := w.Write([]byte("complete\nincomplete"))
	require.NoError(t, err)
	assert.Equal(t, []string{"complete"}, lines)
}

func TestLineWriterHandlesCarriageReturns(t *testing.T) {
	var lines []string
	w := &lineWriter{fn: func(line string) { lines = append(lines, line) }}

	_, err := w.Write([]byte("one\r\ntwo\n"))
	require.NoError(t, err)
	assert.Equal(t, []string{"one", "two"}, lines)
	assert.NotContains(t, strings.Join(lines, ""), "\r")
}
