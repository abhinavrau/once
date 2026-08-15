package command

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/basecamp/once/internal/docker"
)

// resticBackendEnvVars are the backend credentials restic reads from its own
// environment. A bare-metal recovery has no configuration to name them, so
// Once passes through whichever of them it holds itself - the same way
// `backup enable --offsite-env` does, and by name only: values never touch
// argv (ADR-0013). Named ceiling: credentials restic reads from a file
// (AWS_PROFILE, GOOGLE_APPLICATION_CREDENTIALS) are not passed, because the
// file they name is not inside the restic container.
var resticBackendEnvVars = []string{
	"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_DEFAULT_REGION",
	"B2_ACCOUNT_ID", "B2_ACCOUNT_KEY",
	"AZURE_ACCOUNT_NAME", "AZURE_ACCOUNT_KEY", "AZURE_ACCOUNT_SAS",
	"RESTIC_REST_USERNAME", "RESTIC_REST_PASSWORD",
}

type restoreCommand struct {
	cmd      *cobra.Command
	repo     string
	list     bool
	snapshot string
}

func newRestoreCommand() *restoreCommand {
	r := &restoreCommand{}
	r.cmd = &cobra.Command{
		Use:   "restore [<filename> | <app>]",
		Short: "Restore an application from a backup file or a backup repository",
		Long: `Restore an application.

  once restore <filename>                    from a backup file
  once restore --repo <url>                  list what a repository holds
  once restore <app> --repo <url> --list     list one app's snapshots
  once restore <app> [--repo <url>]          restore the newest snapshot

The repository password is taken from RESTIC_PASSWORD, or asked for without
echo. Backend credentials are read from Once's own environment.`,
		Args: cobra.MaximumNArgs(1),
		RunE: WithNamespace(r.run),
	}
	r.cmd.Flags().StringVar(&r.repo, "repo", "", "backup repository to restore from: an absolute path or a restic URL (default: the configured local repository)")
	r.cmd.Flags().BoolVar(&r.list, "list", false, "list the application's snapshots instead of restoring one")
	r.cmd.Flags().StringVar(&r.snapshot, "snapshot", "", "short id of the snapshot to restore (default: the newest)")
	return r
}

// Private

func (r *restoreCommand) run(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
	// A backup file is what it has always been: an existing path. The repo
	// flags settle the ambiguity the other way, so a file that happens to
	// share an application's name can never silently swallow a --repo.
	if len(args) == 1 && !r.repoFlagsSet() {
		if _, err := os.Stat(args[0]); err == nil {
			return restoreFromFile(ctx, ns, args[0])
		}
	}
	return r.runFromRepo(ctx, ns, args)
}

func (r *restoreCommand) repoFlagsSet() bool {
	return r.repo != "" || r.list || r.snapshot != ""
}

func (r *restoreCommand) runFromRepo(ctx context.Context, ns *docker.Namespace, args []string) error {
	cfg := ns.BackupRepoSettings()
	repo, err := r.resolveRepo(cfg)
	if err != nil {
		return err
	}

	snapshots, err := ns.ListRepoSnapshots(ctx, repo)
	if err != nil {
		return err
	}

	if len(args) == 0 {
		if r.list || r.snapshot != "" {
			return fmt.Errorf("--list and --snapshot need an application name; run `once restore --repo %s` to see them", repo.URL)
		}
		fmt.Print(formatRepoListing(repo.URL, snapshots.Apps()))
		return nil
	}

	appName := args[0]
	history := snapshots.ForApp(appName)
	if len(history) == 0 {
		return fmt.Errorf("no snapshots of %q in %s; run `once restore --repo %s` to see what it holds", appName, repo.URL, repo.URL)
	}
	if r.list {
		fmt.Print(formatSnapshotHistory(appName, repo.URL, history))
		return nil
	}

	pick, err := r.pickSnapshot(history, appName)
	if err != nil {
		return err
	}

	fmt.Print(formatRestorePick(repo.URL, pick))
	if err := ns.Setup(ctx); err != nil {
		return fmt.Errorf("setting up namespace: %w", err)
	}

	var app *docker.Application
	if err := runWithProgress("Restoring "+appName, func(docker.DeployProgressCallback) error {
		var err error
		app, err = ns.RestoreFromSnapshot(ctx, repo, pick)
		return err
	}); err != nil {
		return err
	}

	fmt.Print(formatRestoreCompletion(app, pick))
	return r.offerResumeBackups(ctx, ns, repo, cfg)
}

// resolveRepo settles which repository is read. A configured install defaults
// to its local repo and never falls back to the offsite one: silently reading
// the other copy hides which of the two was trusted.
func (r *restoreCommand) resolveRepo(cfg docker.BackupRepoSettings) (docker.RestoreRepo, error) {
	repo := docker.RestoreRepo{URL: r.repo, StagingDir: cfg.StagingDirOrDefault()}
	if repo.URL == "" {
		repo.URL = cfg.LocalPath
	}
	if repo.URL == "" {
		return repo, errors.New("no backup repository: pass --repo <url>, or a backup file to restore from")
	}
	if err := docker.ValidateRepoURL(repo.URL); err != nil {
		return repo, err
	}

	password, err := resolveRepoPassword(repo.URL, cfg)
	if err != nil {
		return repo, err
	}
	repo.Password = password
	repo.Env = restoreRepoEnv(repo.URL, cfg)
	return repo, nil
}

// pickSnapshot chooses from one application's history, which the caller has
// already narrowed.
func (r *restoreCommand) pickSnapshot(history docker.RepoSnapshots, appName string) (docker.RepoSnapshot, error) {
	if r.snapshot == "" {
		return history[len(history)-1], nil
	}
	for _, snapshot := range history {
		if snapshot.ID == r.snapshot {
			return snapshot, nil
		}
	}
	return docker.RepoSnapshot{}, fmt.Errorf("no snapshot %s of %s; run the same command with --list to see the ids", r.snapshot, appName)
}

// offerResumeBackups closes the trap of recovering onto a host that then never
// backs up again.
func (r *restoreCommand) offerResumeBackups(ctx context.Context, ns *docker.Namespace, repo docker.RestoreRepo, cfg docker.BackupRepoSettings) error {
	if !shouldOfferResume(repo.URL, cfg) {
		return nil
	}
	if !isTerminal() || !confirm("Resume backups to this repo?") {
		fmt.Printf("Backups are not enabled. Run `RESTIC_PASSWORD=<the password you just used> once backup enable --offsite-url %s` to back up to this repository.\n", repo.URL)
		return nil
	}

	settings := docker.BackupRepoSettings{
		LocalPath:  docker.DefaultBackupLocalPath,
		OffsiteURL: repo.URL,
		OffsiteEnv: repo.Env,
		Password:   repo.Password,
	}
	if err := runWithProgress("Setting up backups", func(docker.DeployProgressCallback) error {
		return ns.ResumeBackupsAfterRestore(ctx, settings)
	}); err != nil {
		return err
	}
	settings = ns.BackupRepoSettings()

	confirmed, err := takeCustody(settings)
	if err != nil || !confirmed {
		return err
	}
	if err := ns.EnableBackups(ctx); err != nil {
		return fmt.Errorf("enabling backups: %w", err)
	}
	fmt.Printf("Backups enabled. Local repository: %s\nOffsite repository: %s\n", settings.LocalPath, settings.OffsiteURL)
	return nil
}

// Helpers

func restoreFromFile(ctx context.Context, ns *docker.Namespace, filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("opening backup file: %w", err)
	}
	defer file.Close()

	if err := ns.Setup(ctx); err != nil {
		return fmt.Errorf("setting up namespace: %w", err)
	}

	app, err := ns.Restore(ctx, file)
	if err != nil {
		return fmt.Errorf("restoring application: %w", err)
	}

	fmt.Printf("Restored %s from %s\n", app.Settings.Name, filename)
	return nil
}

// resolveRepoPassword: a repository this install already knows comes with its
// password; anything else is the bare-metal case, where the password is the
// user's to supply. RESTIC_PASSWORD is taken verbatim - it is machine-fed -
// while a typed password gets the same normalization the custody gate applies,
// so the dashes and the case of a recovery kit read off paper do not matter.
// A repository Once did not create, with a password that is not Once-shaped,
// is reachable through RESTIC_PASSWORD.
func resolveRepoPassword(repoURL string, cfg docker.BackupRepoSettings) (string, error) {
	if cfg.Password != "" && (repoURL == cfg.LocalPath || repoURL == cfg.OffsiteURL) {
		return cfg.Password, nil
	}
	if password := os.Getenv("RESTIC_PASSWORD"); password != "" {
		return password, nil
	}
	if !isTerminal() {
		return "", errors.New("no password for the backup repository: set RESTIC_PASSWORD, or run this on a terminal to be asked for it")
	}

	typed, err := promptNoEcho("Repository password: ")
	if err != nil {
		return "", err
	}
	if typed == "" {
		return "", errors.New("no password given")
	}
	return docker.NormalizeRepoPassword(typed), nil
}

// shouldOfferResume: only an install with no Backup Repo of its own is offered
// one. An install that already has a repo - enabled or merely configured - has
// a password and a local path of its own, and resuming would have to overwrite
// both with the recovered repo's, which is a decision `once backup enable`
// should be asked for explicitly rather than folded into a restore.
func shouldOfferResume(repoURL string, cfg docker.BackupRepoSettings) bool {
	return !cfg.Enabled && !cfg.Configured() && repoURL != cfg.LocalPath
}

// restoreRepoEnv gathers the backend credentials for the repository. The
// stored ones come first so a configured install can read its own offsite repo
// with no ceremony; Once's own environment then wins, because exporting a
// variable is a deliberate act. A repository that is a filesystem path reaches
// no backend, so it carries no credentials - harvesting them would store
// values the user never named against a repo that cannot use them.
func restoreRepoEnv(repoURL string, cfg docker.BackupRepoSettings) map[string]string {
	if docker.RepoOnDisk(repoURL) {
		return nil
	}

	env := map[string]string{}
	if repoURL == cfg.OffsiteURL {
		maps.Copy(env, cfg.OffsiteEnv)
	}
	for _, name := range resticBackendEnvVars {
		if value := os.Getenv(name); value != "" {
			env[name] = value
		}
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

func formatRepoListing(repoURL string, apps []docker.RepoApp) string {
	if len(apps) == 0 {
		return fmt.Sprintf("%s holds no application backups.\n", repoURL)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Applications in %s:\n", repoURL)
	for _, app := range apps {
		fmt.Fprintf(&b, "  %-20s %2d %-9s  newest %s  %s\n",
			app.Name, app.Snapshots, pluralize("snapshot", app.Snapshots),
			app.Newest.Local().Format("2006-01-02 15:04"), app.Image)
	}
	fmt.Fprintf(&b, "\nRestore one with: once restore <app> --repo %s\n", repoURL)
	return b.String()
}

func formatSnapshotHistory(appName, repoURL string, history docker.RepoSnapshots) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Snapshots of %s in %s:\n", appName, repoURL)
	for _, snapshot := range history {
		fmt.Fprintf(&b, "  %s %s  %s\n", snapshot.ID, snapshot.Time.Local().Format("2006-01-02 15:04"), snapshot.Image)
	}
	fmt.Fprintf(&b, "\nRestore one with: once restore %s --repo %s --snapshot <id>\n", appName, repoURL)
	return b.String()
}

// formatRestorePick shows what is about to be restored and then proceeds with
// no confirmation: a restore is additive - a new application that refuses to
// take another's hostname - so there is nothing here to destroy.
func formatRestorePick(repoURL string, snapshot docker.RepoSnapshot) string {
	return fmt.Sprintf("Restoring %s from %s\n  snapshot %s taken %s on host %s\n  image %s\n",
		snapshot.App, repoURL, snapshot.ID, snapshot.Time.Local().Format("2006-01-02 15:04"), snapshot.Host, snapshot.Image)
}

func formatRestoreCompletion(app *docker.Application, snapshot docker.RepoSnapshot) string {
	return fmt.Sprintf("Restored %s from snapshot %s (%s, %s)\n  %s\n",
		app.Settings.Name, snapshot.ID, snapshot.Time.Local().Format("2006-01-02"), snapshot.Image, app.URL())
}

func pluralize(word string, count int) string {
	if count == 1 {
		return word
	}
	return word + "s"
}

// confirm asks a yes/no question defaulting to yes. Anything that is not a
// terminal never reaches here: a scripted restore must not hang on a prompt.
func confirm(question string) bool {
	fmt.Printf("%s [Y/n] ", question)
	return readConfirmation(os.Stdin)
}

func readConfirmation(in io.Reader) bool {
	answer, err := bufio.NewReader(in).ReadString('\n')
	// A closed stream with an answer on it still counts: err is io.EOF when
	// the last line carries no newline.
	if err != nil && answer == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "", "y", "yes":
		return true
	default:
		return false
	}
}

// promptNoEcho reads a secret from the terminal with echo off. This is the one
// secret Once reads in from the CLI (ADR-0013): bare-metal recovery has no TUI
// to offer, and no progress program owns the terminal at this point.
func promptNoEcho(prompt string) (string, error) {
	fmt.Print(prompt)
	secret, err := term.ReadPassword(os.Stdin.Fd())
	fmt.Println()
	if err != nil {
		return "", fmt.Errorf("reading the password: %w", err)
	}
	return strings.TrimSpace(string(secret)), nil
}

