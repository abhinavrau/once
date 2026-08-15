package docker

import (
	"bytes"
	"encoding/json"
	"strings"
)

// resticStatus is one of restic's `--json` progress lines during a backup -
// about ten a second. Only the fraction matters here; the last `summary` line
// is still what decides the outcome, as everywhere else.
type resticStatus struct {
	MessageType string  `json:"message_type"`
	PercentDone float64 `json:"percent_done"`
	BytesDone   int64   `json:"bytes_done"`
	TotalBytes  int64   `json:"total_bytes"`
}

// lineWriter splits a stream into lines and hands each to fn as it arrives.
// The Docker log stream is chunked by the daemon, never by restic's lines, so
// a live reader has to reassemble them itself.
type lineWriter struct {
	fn  func(string)
	buf bytes.Buffer
}

// Write never short-writes and never fails: it is teed alongside the writer
// that accumulates the real output, and stalling that to report progress
// would be exactly backwards.
func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		line, err := w.buf.ReadString('\n')
		if err != nil {
			// An unterminated tail waits for the rest of its line, which may
			// never come if the container died mid-write.
			w.buf.WriteString(line)
			break
		}
		w.fn(strings.TrimRight(line, "\r\n"))
	}
	return len(p), nil
}

// Helpers

// resticStatusReporter adapts restic's status stream to the progress seam: the
// offsite upload is the one backup step a person waits on, so it is the one
// that reports a transfer percentage rather than a spinner. Returns nil when
// there is nobody to report to - nil is the legitimate headless adapter
// (ADR-0012), and it must cost nothing.
func resticStatusReporter(progress DeployProgressCallback, repoName string) func(string) {
	if progress == nil {
		return nil
	}
	return func(line string) {
		status, ok := parseResticStatus(line)
		if !ok {
			return
		}
		progress(DeployProgress{
			Stage:      DeployStageUploading,
			Percentage: min(max(int(status.PercentDone*100), 0), 100),
			Status:     "uploading " + repoName,
		})
	}
}

// parseResticStatus reads one status line, ignoring everything else on the
// stream: summaries, errors, and the occasional non-JSON word from a backend.
func parseResticStatus(line string) (resticStatus, bool) {
	var status resticStatus
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &status); err != nil {
		return resticStatus{}, false
	}
	return status, status.MessageType == "status"
}
