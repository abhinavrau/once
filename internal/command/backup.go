package command

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/once/internal/docker"
)

type backupCommand struct {
	cmd *cobra.Command
}

func newBackupCommand() *backupCommand {
	b := &backupCommand{}
	b.cmd = &cobra.Command{
		Use:   "backup <host> <filename>",
		Short: "Backup an application to a file",
		Args:  cobra.ExactArgs(2),
		RunE:  WithNamespace(b.run),
	}
	// Cobra routes a first argument matching a subcommand to that subcommand
	// and falls through to this RunE otherwise, so the two-argument export
	// form coexists with the subcommands. Named ceiling: an application whose
	// host is literally "enable", "disable" or "status" cannot be exported.
	b.cmd.AddCommand(newBackupEnableCommand().cmd)
	b.cmd.AddCommand(newBackupDisableCommand().cmd)
	b.cmd.AddCommand(newBackupStatusCommand().cmd)
	return b
}

type backupEnableCommand struct {
	cmd       *cobra.Command
	localPath string
}

func newBackupEnableCommand() *backupEnableCommand {
	e := &backupEnableCommand{}
	e.cmd = &cobra.Command{
		Use:   "enable",
		Short: "Enable scheduled backups to a restic repository",
		Args:  cobra.NoArgs,
		RunE:  WithNamespace(e.run),
	}
	e.cmd.Flags().StringVar(&e.localPath, "local-path", docker.DefaultBackupLocalPath, "path for the local backup repository")
	return e
}

type backupDisableCommand struct {
	cmd *cobra.Command
}

func newBackupDisableCommand() *backupDisableCommand {
	d := &backupDisableCommand{}
	d.cmd = &cobra.Command{
		Use:   "disable",
		Short: "Disable scheduled backups (configuration is kept)",
		Args:  cobra.NoArgs,
		RunE:  WithNamespace(d.run),
	}
	return d
}

type backupStatusCommand struct {
	cmd *cobra.Command
}

func newBackupStatusCommand() *backupStatusCommand {
	s := &backupStatusCommand{}
	s.cmd = &cobra.Command{
		Use:   "status",
		Short: "Show backup configuration and recent results",
		Args:  cobra.NoArgs,
		RunE:  WithNamespace(s.run),
	}
	return s
}

// Private

func (b *backupCommand) run(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
	host := args[0]
	filename, err := filepath.Abs(args[1])
	if err != nil {
		return fmt.Errorf("resolving path: %w", err)
	}

	dir := filepath.Dir(filename)
	name := filepath.Base(filename)

	err = withApplication(ns, host, "backing up", func(app *docker.Application) error {
		return app.BackupToFile(ctx, dir, name)
	})
	if err != nil {
		return err
	}

	fmt.Printf("Backed up %s to %s\n", host, filename)
	return nil
}

func (e *backupEnableCommand) run(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
	if !filepath.IsAbs(e.localPath) {
		return docker.ErrBackupPathRelative
	}

	settings := ns.BackupRepoSettings()
	newPassword := settings.Password == ""
	if newPassword {
		password, err := docker.GenerateRepoPassword()
		if err != nil {
			return fmt.Errorf("generating repository password: %w", err)
		}
		settings.Password = password
	}
	settings.LocalPath = e.localPath
	settings.Enabled = true

	if err := ns.SetBackupRepoSettings(ctx, settings); err != nil {
		return fmt.Errorf("saving backup settings: %w", err)
	}

	fmt.Printf("Backups enabled. Local repository: %s\n", settings.LocalPath)
	if newPassword {
		printRepoPasswordBanner(settings)
	}
	return nil
}

func (d *backupDisableCommand) run(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
	settings := ns.BackupRepoSettings()
	if !settings.Enabled {
		fmt.Println("Backups are not enabled.")
		return nil
	}

	settings.Enabled = false
	if err := ns.SetBackupRepoSettings(ctx, settings); err != nil {
		return fmt.Errorf("saving backup settings: %w", err)
	}

	fmt.Println("Backups disabled. The repository, its password, and this configuration are kept; run `once backup enable` to resume.")
	return nil
}

func (s *backupStatusCommand) run(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
	settings := ns.BackupRepoSettings()
	if !settings.Configured() {
		fmt.Println("Backups are not configured. Run `once backup enable` to set them up.")
		return nil
	}

	if settings.Enabled {
		fmt.Println("Backups: enabled")
	} else {
		fmt.Println("Backups: configured but not enabled")
	}
	fmt.Printf("Local repository: %s\n", settings.LocalPath)

	state, err := ns.LoadState(ctx)
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	for _, app := range ns.Applications() {
		name := app.Settings.Name
		if !app.Settings.Backup.AutoBackup {
			fmt.Printf("  %s: not included in backups\n", name)
			continue
		}
		fmt.Printf("  %s: %s\n", name, describeOperationResult(state.AppState(name).LastBackupResult()))
	}

	if check, ok := state.LastCheck["local"]; ok {
		fmt.Printf("Last repository check: %s\n", describeOperationResult(&check))
	}

	return nil
}

// Helpers

// printRepoPasswordBanner is the no-TTY custody fallback: it prints the
// password loudly, once. The interactive custody flow (type-it-back gate, QR
// delivery) builds on top of this and never replaces it - scripted enables
// must keep working.
func printRepoPasswordBanner(settings docker.BackupRepoSettings) {
	line := strings.Repeat("=", 64)
	fmt.Println()
	fmt.Println(line)
	fmt.Println("  BACKUP REPOSITORY PASSWORD - shown once, never recoverable")
	fmt.Println()
	fmt.Printf("      %s\n", formatRepoPassword(settings.Password))
	fmt.Println()
	fmt.Println("  Store it somewhere safe now (a password manager, printed).")
	fmt.Println("  Without it, no backup made to this repository can ever be")
	fmt.Println("  restored - not by you, and not by Once.")
	fmt.Println(line)
	fmt.Println()
}

// formatRepoPassword groups the password for reading; separators are cosmetic
// and stripped on entry.
func formatRepoPassword(p string) string {
	var groups []string
	for i := 0; i < len(p); i += 4 {
		groups = append(groups, p[i:min(i+4, len(p))])
	}
	return strings.Join(groups, "-")
}

func describeOperationResult(r *docker.OperationResult) string {
	if r == nil {
		return "never backed up"
	}
	when := r.At.Format("2006-01-02 15:04")
	if r.Error != "" {
		return fmt.Sprintf("failed at %s: %s", when, r.Error)
	}
	return "ok at " + when
}
