package command

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/basecamp/once/internal/docker"
	"github.com/basecamp/once/internal/service"
)

type tailscaleCommand struct {
	cmd           *cobra.Command
	clientID      string
	clientSecret  string
	tag           string
	yes           bool
	noPolicyWrite bool
	json          bool
	duration      time.Duration
}

func newTailscaleCommand() *tailscaleCommand {
	t := &tailscaleCommand{}
	t.cmd = &cobra.Command{
		Use:   "tailscale",
		Short: "Manage Tailscale integration",
	}

	enable := &cobra.Command{
		Use:   "enable",
		Short: "Enable Tailscale and boot the Docktail containers",
		RunE:  withLegacyTSDProxyWarning(t.enable),
	}
	enable.Flags().StringVar(&t.clientID, "client-id", "", "oauth_keys-scoped bootstrap OAuth client ID; Once mints the runtime and policy clients from it")
	enable.Flags().StringVar(&t.clientSecret, "client-secret", "", "bootstrap OAuth client secret")
	enable.Flags().StringVar(&t.tag, "tag", "tag:once", "Tailscale tag the minted OAuth clients own")
	enable.Flags().BoolVar(&t.yes, "yes", false, "apply the tailnet policy changes without prompting")
	enable.Flags().BoolVar(&t.noPolicyWrite, "no-policy-write", false, "decline the tailnet policy write and print the snippet to apply manually")
	_ = enable.MarkFlagRequired("client-id")
	_ = enable.MarkFlagRequired("client-secret")
	enable.MarkFlagsMutuallyExclusive("yes", "no-policy-write")

	disable := &cobra.Command{
		Use:   "disable",
		Short: "Disable Tailscale and remove the Docktail containers",
		RunE:  withLegacyTSDProxyWarning(t.disable),
	}

	status := &cobra.Command{
		Use:   "status",
		Short: "Show each exposed app's tailnet URL and advertised/approved state",
		RunE:  withLegacyTSDProxyWarning(t.status),
	}
	status.Flags().BoolVar(&t.json, "json", false, "emit structured JSON instead of prose")

	t.cmd.AddCommand(enable, disable, status, t.newFunnelCommand())
	return t
}

func (t *tailscaleCommand) newFunnelCommand() *cobra.Command {
	funnel := &cobra.Command{
		Use:   "funnel",
		Short: "Manage temporary public Funnel access for an app",
	}

	enable := &cobra.Command{
		Use:   "enable <app-name>",
		Short: "Grant temporary public access to an app via Tailscale Funnel",
		Args:  cobra.ExactArgs(1),
		RunE:  withLegacyTSDProxyWarning(t.funnelEnable),
	}
	enable.Flags().DurationVar(&t.duration, "duration", docker.DefaultFunnelDuration, "how long the Funnel stays open (max 24h)")

	disable := &cobra.Command{
		Use:   "disable <app-name>",
		Short: "Revoke an app's public Funnel access immediately",
		Args:  cobra.ExactArgs(1),
		RunE:  withLegacyTSDProxyWarning(t.funnelDisable),
	}

	funnel.AddCommand(enable, disable)
	return funnel
}

// Private

func (t *tailscaleCommand) enable(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
	settings := docker.TailscaleSettings{Tag: t.tag}
	if !docker.TailscaleControlSeamActive() {
		var err error
		settings.ClientID, settings.ClientSecret, err = t.mintFromBootstrap(ctx)
		if err != nil {
			return err
		}
	}
	warnings, err := ns.EnableTailscale(ctx, settings)
	printTailscaleWarnings(warnings)
	if err != nil {
		return err
	}

	fmt.Println("Tailscale enabled")
	return nil
}

func (t *tailscaleCommand) disable(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
	if err := ns.DisableTailscale(ctx); err != nil {
		return err
	}

	fmt.Println("Tailscale disabled")
	return nil
}

// tailscaleStatusReport is the stable JSON shape of `tailscale status --json`.
// An empty DomainSuffix on an enabled tailnet means Magic DNS is not known yet
// and no service states could be read.
type tailscaleStatusReport struct {
	Enabled      bool                   `json:"enabled"`
	DomainSuffix string                 `json:"domainSuffix,omitempty"`
	Services     []docker.ServiceStatus `json:"services,omitempty"`
}

// status lists once-admin and each exposed app with its service URL (from the
// Magic DNS suffix stored at enable time) and its advertised/approved state,
// read live from the local daemon and the Tailscale cloud API.
func (t *tailscaleCommand) status(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
	report, err := t.statusReport(ctx, ns)
	if err != nil {
		return err
	}
	if t.json {
		return printJSON(report)
	}

	if !report.Enabled {
		fmt.Println("Tailscale is not enabled")
		return nil
	}
	if report.DomainSuffix == "" {
		fmt.Println("The tailnet's Magic DNS suffix is not known yet (the tailnet may still be spinning up); re-run in a moment")
		return nil
	}
	for _, s := range report.Services {
		fmt.Printf("%s\t%s\t%s\n", s.Name, s.URL, s.State)
	}
	return nil
}

func (t *tailscaleCommand) statusReport(ctx context.Context, ns *docker.Namespace) (tailscaleStatusReport, error) {
	var report tailscaleStatusReport

	enabled, err := ns.Tailscale().Enabled(ctx)
	if err != nil {
		return report, fmt.Errorf("checking Tailscale: %w", err)
	}
	report.Enabled = enabled
	if !enabled {
		return report, nil
	}

	settings, err := ns.Tailscale().LoadSettings(ctx)
	if err != nil {
		return report, fmt.Errorf("reading Tailscale settings: %w", err)
	}
	report.DomainSuffix = settings.DomainSuffix
	if settings.DomainSuffix == "" {
		return report, nil
	}

	names := []string{"once-admin"}
	var stopped []string
	for _, app := range ns.Applications() {
		if app.Settings.Exposure != docker.ExposureTailnet {
			continue
		}
		if app.Running {
			names = append(names, app.Settings.Host)
		} else {
			stopped = append(stopped, app.Settings.Host)
		}
	}

	statuses, err := ns.Tailscale().ServiceStatuses(ctx, names)
	if err != nil {
		return report, err
	}
	for _, name := range stopped {
		statuses = append(statuses, docker.ServiceStatus{Name: name, URL: settings.TailnetURL(name), State: "not running"})
	}
	report.Services = statuses
	return report, nil
}

func (t *tailscaleCommand) funnelEnable(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
	if err := docker.ValidateFunnelDuration(t.duration); err != nil {
		return err
	}

	enabled, err := ns.Tailscale().Enabled(ctx)
	if err != nil {
		return fmt.Errorf("checking Tailscale: %w", err)
	}
	if !enabled {
		return fmt.Errorf("run `once tailscale enable` first; Tailscale is not enabled")
	}

	// Daemon required: without it, the automatic teardown at expiry could never
	// run and the app would stay public indefinitely.
	if err := requireBackgroundDaemon(ns.Name()); err != nil {
		return err
	}

	app := ns.Application(args[0])
	if app == nil {
		return fmt.Errorf("no application named %q", args[0])
	}

	expiresAt := time.Now().Add(t.duration)
	if err := app.EnableFunnel(ctx, expiresAt); err != nil {
		return fmt.Errorf("enabling Funnel: %w", err)
	}

	// Surface activation failures: Funnel needs the tailnet ACL's funnel node
	// attribute, which Once can't manage. Only report it active once the local
	// tailscaled confirms a Funnel proxying to this app's container.
	if err := ns.Tailscale().WaitForFunnelActive(ctx, app); err != nil {
		return err
	}

	fmt.Printf("Funnel enabled for %s until %s\n", app.Settings.Name, expiresAt.Format(time.RFC3339))
	return nil
}

func (t *tailscaleCommand) funnelDisable(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
	app := ns.Application(args[0])
	if app == nil {
		return fmt.Errorf("no application named %q", args[0])
	}

	if !app.Settings.FunnelEnabled() {
		fmt.Printf("Funnel is not active for %s\n", app.Settings.Name)
		return nil
	}

	if err := app.DisableFunnel(ctx); err != nil {
		return fmt.Errorf("disabling Funnel: %w", err)
	}

	fmt.Printf("Funnel disabled for %s\n", app.Settings.Name)
	return nil
}

// mintFromBootstrap turns the one Bootstrap Credential into the clients enable
// needs: the policy offer runs first with a one-shot minted policy_file client
// (a fresh tailnet must define the tag before a tagged client can be minted),
// then the minimal-scope runtime client is minted and returned. The bootstrap
// and policy credentials live only in memory; only the runtime credential is
// persisted (by enable, in the once label).
func (t *tailscaleCommand) mintFromBootstrap(ctx context.Context) (string, string, error) {
	if err := t.requirePolicyDecision(); err != nil {
		return "", "", err
	}
	bootstrap, err := docker.NewTailscaleBootstrap(ctx, t.clientID, t.clientSecret)
	if err != nil {
		return "", "", err
	}
	t.offerPolicyWrite(ctx, bootstrap)
	return bootstrap.MintRuntimeClient(ctx, t.tag)
}

// requirePolicyDecision fails fast when a run could reach the policy consent
// prompt without a terminal to show it on: a non-TTY run must decide up front
// via --yes or --no-policy-write instead of hanging.
func (t *tailscaleCommand) requirePolicyDecision() error {
	if t.yes || t.noPolicyWrite || term.IsTerminal(os.Stdin.Fd()) {
		return nil
	}
	return fmt.Errorf("stdin is not a terminal, so the tailnet policy consent prompt cannot be shown; pass --yes to apply the policy changes or --no-policy-write to print the snippet for manual application instead")
}

// offerPolicyWrite offers to write the four tailnet policy sections Once needs
// via the Tailscale API, so the user never hand-edits HuJSON. Best effort by
// design: any decline or failure prints the copy-paste snippet and enable
// proceeds, with the credential probe as the enforcement. --no-policy-write
// declines before any policy client is minted; otherwise the one-shot policy
// client is deleted again as soon as the offer resolves.
func (t *tailscaleCommand) offerPolicyWrite(ctx context.Context, bootstrap *docker.TailscaleBootstrap) {
	if t.noPolicyWrite {
		printPolicySnippet(t.tag)
		return
	}

	clientID, clientSecret, err := bootstrap.MintPolicyClient(ctx)
	if err != nil {
		fmt.Println("Could not mint the policy OAuth client:", err)
		printPolicySnippet(t.tag)
		return
	}
	defer func() {
		if err := bootstrap.DeleteClient(ctx, clientID); err != nil {
			fmt.Println(docker.PolicyRevokeFailureNotice+":", err)
		}
	}()

	t.provisionPolicy(ctx, clientID, clientSecret)
}

// provisionPolicy plans the policy patch with the one-shot policy credential,
// shows the diff and GitOps warning, and applies on consent: --yes up front,
// or the prompt. Any decline or failure prints the snippet and enable proceeds.
func (t *tailscaleCommand) provisionPolicy(ctx context.Context, clientID, clientSecret string) {
	update, err := docker.PlanPolicyUpdate(ctx, clientID, clientSecret, t.tag)
	if err != nil {
		fmt.Println("Could not plan the policy update:", err)
		printPolicySnippet(t.tag)
		return
	}
	if !update.Changed() {
		fmt.Println("The tailnet policy already contains everything Once needs; nothing to change.")
		return
	}

	fmt.Println(docker.PolicyConsentIntro)
	fmt.Println()
	fmt.Print(update.Diff())
	fmt.Println()
	fmt.Println(docker.PolicyGitOpsWarning)
	if !t.yes && !promptYesNo("Write this policy change?") {
		printPolicySnippet(t.tag)
		return
	}
	if err := update.Apply(ctx); err != nil {
		fmt.Println("The policy write failed:", err)
		printPolicySnippet(t.tag)
		return
	}
	fmt.Println("Tailnet policy updated.")
}

// Helpers

// withLegacyTSDProxyWarning runs a tailscale subcommand after surfacing the
// pre-Docktail upgrade message when a legacy once-tsdproxy container is still
// around. Once never migrates or removes it; the user does.
func withLegacyTSDProxyWarning(fn NamespaceRunE) func(*cobra.Command, []string) error {
	return WithNamespace(func(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
		if w := ns.Tailscale().LegacyTSDProxyWarning(ctx); w != "" {
			printTailscaleWarnings([]string{w})
		}
		return fn(ctx, ns, cmd, args)
	})
}

// warnShadowedServiceName surfaces the hostname-shadowing warning for a
// tailnet-exposed app's service name - the collision guard against existing
// tailnet machines.
func warnShadowedServiceName(ctx context.Context, ns *docker.Namespace, settings docker.ApplicationSettings) {
	if settings.Exposure == docker.ExposureTailnet {
		printTailscaleWarnings(ns.Tailscale().ShadowWarnings(ctx, []string{settings.Host}))
	}
}

func printTailscaleWarnings(warnings []string) {
	for _, w := range warnings {
		fmt.Println("Warning:", w)
	}
}

func printPolicySnippet(tag string) {
	fmt.Println()
	fmt.Println("Merge this snippet into your tailnet policy file (https://login.tailscale.com/admin/acls), then re-run `once tailscale enable`:")
	fmt.Println()
	fmt.Println(docker.PolicySnippet(tag))
	fmt.Println()
}

func promptYesNo(question string) bool {
	fmt.Print(question + " [y/N] ")
	var answer string
	_, _ = fmt.Scanln(&answer)
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}

func requireBackgroundDaemon(namespace string) error {
	svc, err := service.New()
	if err != nil {
		return err
	}
	name := namespace + backgroundServiceSuffix
	if !svc.IsInstalled(name) || !svc.IsRunning(name) {
		return fmt.Errorf("the background service is not running; run `once background install` before enabling a Funnel so it can be torn down automatically on expiry")
	}
	return nil
}
