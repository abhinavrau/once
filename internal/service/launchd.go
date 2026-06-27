package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

const (
	launchdDir  = "/Library/LaunchDaemons"
	labelPrefix = "com.basecamp."
)

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>background</string>
		<string>run</string>
		<string>--namespace</string>
		<string>%s</string>
	</array>
%s	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>5</integer>
</dict>
</plist>
`

type Launchd struct{}

func (l *Launchd) IsInstalled(name string) bool {
	_, err := os.Stat(l.plistPath(name))
	return err == nil
}

func (l *Launchd) IsRunning(name string) bool {
	return exec.Command("launchctl", "print", "system/"+l.label(name)).Run() == nil
}

func (l *Launchd) Install(ctx context.Context, name, execPath, namespace string) error {
	label := l.label(name)
	path := l.plistPath(name)

	plistContent := renderPlist(label, execPath, namespace, os.Getenv("ONCE_NO_SELF_UPDATE"))

	if err := os.WriteFile(path, []byte(plistContent), 0o644); err != nil {
		return fmt.Errorf("writing plist file: %w", err)
	}

	return l.launchctl(ctx, "bootstrap", "system", path)
}

func (l *Launchd) Remove(ctx context.Context, name string) error {
	label := l.label(name)

	if err := l.launchctl(ctx, "bootout", "system/"+label); err != nil {
		return err
	}

	path := l.plistPath(name)

	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing plist file: %w", err)
	}

	return nil
}

func (l *Launchd) ServiceName(name string) string {
	return l.label(name)
}

// Private

func (l *Launchd) label(name string) string {
	return labelPrefix + name
}

func (l *Launchd) launchctl(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "launchctl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("launchctl %s: %w", args[0], err)
	}
	return nil
}

func (l *Launchd) plistPath(name string) string {
	return filepath.Join(launchdDir, l.label(name)+".plist")
}

// Helpers

// renderPlist builds the launchd plist. When noSelfUpdate is set, it injects an
// EnvironmentVariables dict carrying ONCE_NO_SELF_UPDATE so the daemon's
// self-update stays disabled across reinstalls (the plist is rewritten on every
// `background install`). Absent the var, the block is omitted.
func renderPlist(label, execPath, namespace, noSelfUpdate string) string {
	var env string
	if noSelfUpdate != "" {
		env = fmt.Sprintf("\t<key>EnvironmentVariables</key>\n\t<dict>\n\t\t<key>ONCE_NO_SELF_UPDATE</key>\n\t\t<string>%s</string>\n\t</dict>\n", noSelfUpdate)
	}
	return fmt.Sprintf(plistTemplate, label, execPath, namespace, env)
}
