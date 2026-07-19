package logging

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/basecamp/once/internal/fsutil"
)

var level slog.LevelVar

// SetVerbose toggles the active log level between Debug, revealing the
// per-request lines emitted by WrapTransport, and the default Info.
func SetVerbose(verbose bool) {
	if verbose {
		level.Set(slog.LevelDebug)
	} else {
		level.Set(slog.LevelInfo)
	}
}

func Verbose() bool {
	return level.Level() <= slog.LevelDebug
}

// LogFilePath returns the path logs are written to while the TUI runs.
func LogFilePath() (string, error) {
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "once.log"), nil
}

// ToLogFile switches logging to a file for the duration of fn, then restores
// stderr logging when fn returns.
func ToLogFile(fn func() error) error {
	path, err := LogFilePath()
	if err != nil {
		slog.Warn("Could not determine log directory", "error", err)
		return fn()
	}

	file, err := fsutil.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		slog.Warn("Could not open log file", "error", err)
		return fn()
	}

	defer func() {
		SetupStderr()
		file.Close()
	}()

	slog.SetDefault(slog.New(slog.NewTextHandler(file, &slog.HandlerOptions{Level: &level})))

	return fn()
}

func SetupStderr() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: &level})))
}

// Helpers

func stateDir() (string, error) {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "once"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, ".local", "state", "once"), nil
}
