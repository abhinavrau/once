package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basecamp/once/internal/docker"
	"github.com/basecamp/once/internal/system"
	"github.com/basecamp/once/internal/ui"
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
	cmd              *cobra.Command
	localPath        string
	offsiteURL       string
	offsiteEnv       []string
	passwordDelivery string
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
	e.cmd.Flags().StringVar(&e.offsiteURL, "offsite-url", "", "restic repository URL for the offsite copy (empty disables the offsite leg)")
	e.cmd.Flags().StringSliceVar(&e.offsiteEnv, "offsite-env", nil, "names of environment variables holding the offsite backend credentials; values are read from the environment, never the command line")
	e.cmd.Flags().StringVar(&e.passwordDelivery, "password-delivery", "", "how to take custody of the repository password without being asked (screen, qr)")
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

	// The export pauses the app container just like the scheduled Backup Run
	// does; two pausers racing tear the SQLite copy, so both take the same
	// host flock.
	lock, err := system.TryLock(docker.BackupLockPath)
	if errors.Is(err, system.ErrLocked) {
		return docker.ErrBackupInProgress
	}
	if err != nil {
		return err
	}
	defer lock.Unlock()

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

// run is the enable sequence: generate password -> validation probe ->
// custody flow -> Enabled = true. The probe needs the password (it inits the
// repos with it); the custody ritual is never demanded for a configuration
// that then fails its probe. On probe failure everything persists, password
// included, with only Enabled withheld.
func (e *backupEnableCommand) run(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
	if err := validatePasswordDelivery(e.passwordDelivery); err != nil {
		return err
	}

	settings, err := e.collectSettings(ns, cmd)
	if err != nil {
		return err
	}
	enabledBefore := ns.BackupRepoSettings().Enabled

	// The probe error comes back verbatim from restic; the settings - and
	// the password ConfigureBackups mints when none exists - are already
	// persisted, so a retry reuses them. Read them back for the kit.
	if err := runWithProgress("Validating backup repositories", func(progress docker.DeployProgressCallback) error {
		return ns.ConfigureBackups(ctx, settings, progress)
	}); err != nil {
		return err
	}
	settings = ns.BackupRepoSettings()

	if !enabledBefore {
		confirmed, err := takeCustody(ctx, ns, settings, ui.CustodyDelivery(e.passwordDelivery))
		if err != nil || !confirmed {
			return err
		}
	}

	if err := ns.EnableBackups(ctx); err != nil {
		return fmt.Errorf("enabling backups: %w", err)
	}
	fmt.Printf("Backups enabled. Local repository: %s\n", settings.LocalPath)
	if settings.OffsiteURL != "" {
		fmt.Printf("Offsite repository: %s\n", settings.OffsiteURL)
	}
	return nil
}

// collectSettings merges the flags over the stored settings: an untouched
// flag keeps what is configured. The password stays as stored (empty on a
// first enable) - ConfigureBackups mints it when missing and reuses it
// otherwise, so a retry hands over the same password the repos were
// initialized with - unless RESTIC_PASSWORD names one, which is how an
// install enrolls a repository that already exists. Without it, a recovered
// host pointed at the repo it just restored from would mint a fresh password
// and fail to open it.
func (e *backupEnableCommand) collectSettings(ns *docker.Namespace, cmd *cobra.Command) (docker.BackupRepoSettings, error) {
	settings := ns.BackupRepoSettings()

	settings.Password = enableRepoPassword(settings.Password)
	if cmd.Flags().Changed("local-path") || settings.LocalPath == "" {
		settings.LocalPath = e.localPath
	}
	if !filepath.IsAbs(settings.LocalPath) {
		return settings, docker.ErrBackupPathRelative
	}
	if cmd.Flags().Changed("offsite-url") {
		settings.OffsiteURL = e.offsiteURL
	}
	if err := docker.ValidateOffsiteURL(settings.OffsiteURL); err != nil {
		return settings, err
	}
	if cmd.Flags().Changed("offsite-env") {
		env, err := captureOffsiteEnv(e.offsiteEnv)
		if err != nil {
			return settings, err
		}
		settings.OffsiteEnv = env
	}
	return settings, nil
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
	status, err := ns.BackupStatus(ctx)
	if err != nil {
		return err
	}
	fmt.Print(renderBackupStatus(status))
	return nil
}

// Helpers

// validatePasswordDelivery gates the --password-delivery flag. Whether the QR
// can actually be drawn on this terminal is settled later, by the custody
// flow, which is the first thing that knows the colour profile.
func validatePasswordDelivery(delivery string) error {
	switch ui.CustodyDelivery(delivery) {
	case ui.CustodyDeliveryAsk, ui.CustodyDeliveryScreen, ui.CustodyDeliveryQR:
		return nil
	default:
		return fmt.Errorf("unknown password delivery %q (supported: screen, qr)", delivery)
	}
}

// enableRepoPassword decides which password an enable runs with. A stored one
// always wins - a retry must reuse the password its repos were initialized
// with. Otherwise RESTIC_PASSWORD, which is how an install enrolls a
// repository that already exists: without it a host recovered from a repo and
// pointed back at it would mint a fresh password and fail to open it. Empty
// leaves ConfigureBackups to mint one. Taken verbatim, exactly as
// `once restore` reads it - the two must agree, or an enable would store a
// password that differs from the one that just opened the repository.
func enableRepoPassword(stored string) string {
	if stored != "" {
		return stored
	}
	return os.Getenv("RESTIC_PASSWORD")
}

// captureOffsiteEnv reads the named variables from Once's own environment:
// names are not secrets, values never touch a command line (ADR-0013).
func captureOffsiteEnv(names []string) (map[string]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	env := make(map[string]string, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		value, ok := os.LookupEnv(name)
		if !ok || value == "" {
			return nil, fmt.Errorf("environment variable %s is not set; --offsite-env takes names and reads the values from the environment", name)
		}
		env[name] = value
	}
	return env, nil
}

// printRepoPasswordBanner is the no-TTY custody fallback: it prints the
// recovery kit loudly, once, and never prompts - scripted enables must keep
// working. The interactive custody flow builds on top of this and never
// replaces it.
func printRepoPasswordBanner(kit ui.RecoveryKit) {
	line := strings.Repeat("=", 68)
	fmt.Println()
	fmt.Println(line)
	fmt.Println("  BACKUP PASSWORD - SHOWN ONCE, NEVER AGAIN")
	fmt.Println()
	for l := range strings.Lines(kit.Text()) {
		fmt.Println("  " + strings.TrimRight(l, "\n"))
	}
	fmt.Println()
	fmt.Println("  Every backup is encrypted with this password. Once cannot show")
	fmt.Println("  it again. If this server is lost and you do not have this")
	fmt.Println("  password, every backup is permanently unreadable.")
	fmt.Println()
	fmt.Println("  Save the whole block above somewhere off this machine, now.")
	fmt.Println(line)
	fmt.Println()
}
