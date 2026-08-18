package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/once/internal/docker"
)

// envClientSecret carries the Runtime Credential's secret into `once tailscale
// enable`. It is the only way in: a command line is world-readable through
// /proc/<pid>/cmdline, an environment is not (ADR-0013). It shares the
// ONCE_TAILSCALE_ prefix with the hidden control seam variables but is
// unrelated to them, and is read here rather than in internal/docker.
const envClientSecret = "ONCE_TAILSCALE_CLIENT_SECRET"

type tailscaleCommand struct {
	cmd      *cobra.Command
	clientID string
	// clientSecret is resolved from the environment in enable's PreRunE, before
	// anything touches Docker, so a rejected command line says what to fix
	// instead of failing on an unrelated Docker error first.
	clientSecret string
	tag          string
	mode         string
	json         bool
	duration     time.Duration
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
		Long: `Enable Tailscale and boot the Docktail containers.

Setup, once per tailnet:
 1. Run ` + "`once tailscale policy`" + ` and merge the printed snippet into your
    tailnet policy file (https://login.tailscale.com/admin/acls).
 2. In the admin console (Settings -> Trust credentials), create an OAuth
    client with write access to Auth Keys, Devices Core, and Services,
    tagged with your tag (default tag:once).
 3. Put the client's secret in ` + envClientSecret + ` and run this
    command with --client-id. The secret comes from the environment so that
    it never appears on a command line, which every user on this machine can
    read. The credential is probed before anything boots, then stored for
    Docktail and the sidecar.`,
		Example: "  read -rs " + envClientSecret + " && export " + envClientSecret + `
  once tailscale enable --client-id k123CNTRL`,
		PreRunE: t.resolveClientSecret,
		RunE:    WithNamespace(t.enable),
	}
	enable.Flags().StringVar(&t.clientID, "client-id", "", "OAuth client ID (scopes auth_keys, devices:core, services; tagged)")
	enable.Flags().StringVar(&t.tag, "tag", "tag:once", "Tailscale tag the OAuth client is tagged with")
	enable.Flags().StringVar(&t.mode, "mode", "", "tailscaled to drive: sidecar (Once-managed, the default) or host (an existing host daemon); unset keeps an enabled install's current mode")
	_ = enable.MarkFlagRequired("client-id")
	// Removed flag, kept registered (hidden) so it fails with a clean-break
	// error naming its replacement instead of cobra's "unknown flag". It keeps
	// a string value so `--client-secret=x` reaches that error too.
	enable.Flags().String("client-secret", "", "")
	_ = enable.Flags().MarkHidden("client-secret")

	policy := &cobra.Command{
		Use:   "policy",
		Short: "Print the tailnet policy snippet Once needs",
		Run:   func(cmd *cobra.Command, args []string) { printPolicySnippet(t.tag) },
	}
	policy.Flags().StringVar(&t.tag, "tag", "tag:once", "Tailscale tag the policy sections are built for")

	disable := &cobra.Command{
		Use:   "disable",
		Short: "Disable Tailscale and remove the Docktail containers",
		RunE:  WithNamespace(t.disable),
	}

	status := &cobra.Command{
		Use:   "status",
		Short: "Show each exposed app's tailnet URL and advertised/approved state",
		RunE:  WithNamespace(t.status),
	}
	status.Flags().BoolVar(&t.json, "json", false, "emit structured JSON instead of prose")

	t.cmd.AddCommand(enable, disable, status, policy, t.newFunnelCommand())
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
		RunE:  WithNamespace(t.funnelEnable),
	}
	enable.Flags().DurationVar(&t.duration, "duration", docker.DefaultFunnelDuration, "how long the Funnel stays open (max 24h)")

	disable := &cobra.Command{
		Use:   "disable <app-name>",
		Short: "Revoke an app's public Funnel access immediately",
		Args:  cobra.ExactArgs(1),
		RunE:  WithNamespace(t.funnelDisable),
	}

	funnel.AddCommand(enable, disable)
	return funnel
}

// Private

// resolveClientSecret takes the Runtime Credential's secret from the
// environment, before enable touches Docker. Passing it as --client-secret is
// refused rather than accepted with a warning: the leak the environment closes
// stays open for as long as a working command line exists to copy.
func (t *tailscaleCommand) resolveClientSecret(cmd *cobra.Command, args []string) error {
	if cmd.Flags().Changed("client-secret") {
		return fmt.Errorf("--client-secret is no longer accepted: pass the secret in %s, so it stays out of the command line that every user on this machine can read", envClientSecret)
	}

	t.clientSecret = strings.TrimSpace(os.Getenv(envClientSecret))
	if t.clientSecret == "" {
		return fmt.Errorf("set the OAuth client secret in %s, or run `once` and enable Tailscale from the dashboard", envClientSecret)
	}
	return nil
}

func (t *tailscaleCommand) enable(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
	settings := docker.TailscaleSettings{
		ClientID:     t.clientID,
		ClientSecret: t.clientSecret,
		Tag:          t.tag,
		Mode:         t.mode,
		// No --topology flag yet (ticket 05 adds one, alongside the Nodes
		// topology's own frontend facts): pin the CLI to Services so its
		// behavior is unchanged until then, rather than a fresh enable
		// silently landing on the (not yet implemented) Nodes default.
		Topology: docker.TailnetTopologyServices,
	}
	var warnings []string
	err := runShowingProgress("tailscale enable", func(progress docker.DeployProgressCallback) error {
		var err error
		warnings, err = ns.EnableTailscale(ctx, settings, progress)
		return err
	})
	printWarnings(warnings)
	if errors.Is(err, docker.ErrTagNotInPolicy) {
		printPolicySnippet(t.tag)
	}
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

// stateNotRunning is the row state of a stopped app: its service is
// deregistered, so its URL is expected not to resolve.
const stateNotRunning = "not running"

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
	warnings := tailnetDNSWarnings(ctx, resolvableServiceURLs(report.Services))
	for i, s := range report.Services {
		fmt.Printf("%s\t%s\t%s\n", s.Name, s.URL, s.State)
		if s.FunnelExpiresAt != nil {
			funnel := s.FunnelURL
			if funnel == "" {
				funnel = "public (URL not known yet; re-run in a moment)"
			}
			fmt.Printf("\tFunnel: %s (expires %s)\n", funnel, s.FunnelExpiresAt.Format(time.RFC3339))
		}
		if warnings[i] != "" {
			printWarnings([]string{warnings[i]})
		}
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
		statuses = append(statuses, docker.ServiceStatus{Name: name, URL: settings.TailnetURL(name), State: stateNotRunning})
	}

	funnelApps := map[string]*docker.Application{}
	for _, app := range ns.Applications() {
		if app.Settings.FunnelEnabled() {
			funnelApps[app.Settings.Host] = app
		}
	}
	for i, s := range statuses {
		if app := funnelApps[s.Name]; app != nil {
			statuses[i].FunnelURL = settings.FunnelURL(app.Settings.ActiveFunnelPort())
			statuses[i].FunnelExpiresAt = app.Settings.FunnelExpiresAt
		}
	}

	report.Services = statuses
	return report, nil
}

func (t *tailscaleCommand) funnelEnable(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
	app := ns.ApplicationByNameOrHost(args[0])
	if app == nil {
		return fmt.Errorf("no application named %q", args[0])
	}

	url, expiresAt, err := ns.EnableFunnel(ctx, app, t.duration)
	if err != nil {
		return hintTailscaleEnable(err)
	}

	fmt.Printf("Funnel enabled for %s until %s\n", app.Settings.Name, expiresAt.Format(time.RFC3339))
	fmt.Printf("Public URL: %s (Funnel serves on the node's hostname, not the app's tailnet URL)\n", url)
	return nil
}

func (t *tailscaleCommand) funnelDisable(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
	app := ns.ApplicationByNameOrHost(args[0])
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

// Helpers

// resolvableServiceURLs gives each row's service URL, empty for a row whose
// state already says the service is not there to resolve - warning about those
// would be noise.
func resolvableServiceURLs(services []docker.ServiceStatus) []string {
	urls := make([]string, len(services))
	for i, s := range services {
		if s.State != stateNotRunning {
			urls[i] = s.URL
		}
	}
	return urls
}

// hintTailscaleEnable points a CLI user at the fix when a deploy or Funnel
// enable is refused because Tailscale is not enabled.
func hintTailscaleEnable(err error) error {
	if errors.Is(err, docker.ErrTailscaleNotEnabled) {
		return fmt.Errorf("%w; run `once tailscale enable` first", err)
	}
	return err
}

func printWarnings(warnings []string) {
	for _, w := range warnings {
		fmt.Println("Warning:", w)
	}
}

func printPolicySnippet(tag string) {
	fmt.Println()
	fmt.Println("Merge this snippet into your tailnet policy file (https://login.tailscale.com/admin/acls), then run `once tailscale enable`:")
	fmt.Println()
	fmt.Println(docker.PolicySnippet(tag))
	fmt.Println()
}
