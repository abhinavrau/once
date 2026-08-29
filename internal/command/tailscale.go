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
	// topology is the requested Tailnet Topology (ADR-0015): empty keeps an
	// enabled install's stored topology, or defaults a fresh enable to nodes -
	// docker.ValidateTailscaleTopology/Namespace.EnableTailscale do the actual
	// resolving and validation; this command only carries the flag through.
	topology string
	// policyTopology is separate from topology: `policy` prints a snippet with
	// no install to read a stored topology from, so it needs a real default
	// rather than an empty value with nothing to resolve against. It matches
	// what a fresh enable resolves to (nodes): `policy` is step one of the
	// enable flow, so whoever runs it is by definition not enabled yet.
	policyTopology string
	json           bool
	duration       time.Duration
}

func newTailscaleCommand() *tailscaleCommand {
	t := &tailscaleCommand{}
	t.cmd = &cobra.Command{
		Use:   "tailscale",
		Short: "Manage Tailscale integration",
	}

	nodesFacts := mustTailscaleTopologyFacts(docker.TailnetTopologyNodes)
	servicesFacts := mustTailscaleTopologyFacts(docker.TailnetTopologyServices)

	enable := &cobra.Command{
		Use:   "enable",
		Short: "Enable Tailscale and boot the tailnet topology's containers",
		Long: fmt.Sprintf(`Enable Tailscale and boot the requested tailnet topology's containers
(--topology %s|%s; an already-enabled install keeps its stored topology when
--topology is left unset, a fresh enable becomes %s).

Under --topology %s:
%s

Under --topology %s:
%s

The secret comes from the environment (%s) so that it never appears on a
command line, which every user on this machine can read. The credential is
probed before anything boots.`,
			docker.TailnetTopologyNodes, docker.TailnetTopologyServices, docker.TailnetTopologyNodes,
			docker.TailnetTopologyNodes, nodesFacts.SetupIntro,
			docker.TailnetTopologyServices, servicesFacts.SetupIntro,
			envClientSecret),
		Example: "  read -rs " + envClientSecret + " && export " + envClientSecret + `
  once tailscale enable --client-id k123CNTRL`,
		PreRunE: t.resolveClientSecret,
		RunE:    WithNamespace(t.enable),
	}
	enable.Flags().StringVar(&t.clientID, "client-id", "", "OAuth client ID (tagged; the required scopes depend on --topology - see the setup steps above)")
	enable.Flags().StringVar(&t.tag, "tag", nodesFacts.DefaultSettings.Tag, "Tailscale tag the OAuth client is tagged with")
	enable.Flags().StringVar(&t.mode, "mode", "", fmt.Sprintf(
		"tailscaled to drive under the %s topology (%s, the default, or %s); unset keeps an enabled install's current mode, or that topology's default for a fresh enable; not used under %s",
		docker.TailnetTopologyServices, servicesFacts.ModeChoices[0], servicesFacts.ModeChoices[1], docker.TailnetTopologyNodes))
	enable.Flags().StringVar(&t.topology, "topology", "", fmt.Sprintf(
		"tailnet topology: %s or %s; unset keeps an enabled install's current topology, or defaults a fresh enable to %s",
		docker.TailnetTopologyServices, docker.TailnetTopologyNodes, docker.TailnetTopologyNodes))
	_ = enable.MarkFlagRequired("client-id")
	// Removed flag, kept registered (hidden) so it fails with a clean-break
	// error naming its replacement instead of cobra's "unknown flag". It keeps
	// a string value so `--client-secret=x` reaches that error too.
	enable.Flags().String("client-secret", "", "")
	_ = enable.Flags().MarkHidden("client-secret")

	policy := &cobra.Command{
		Use:   "policy",
		Short: "Print the tailnet policy snippet Once needs",
		RunE:  func(cmd *cobra.Command, args []string) error { return t.policy() },
	}
	policy.Flags().StringVar(&t.tag, "tag", nodesFacts.DefaultSettings.Tag, "Tailscale tag the policy sections are built for")
	policy.Flags().StringVar(&t.policyTopology, "topology", docker.TailnetTopologyNodes, fmt.Sprintf(
		"tailnet topology to print the policy snippet for: %s or %s", docker.TailnetTopologyNodes, docker.TailnetTopologyServices))

	disable := &cobra.Command{
		Use:   "disable",
		Short: "Disable Tailscale and remove its containers",
		RunE:  WithNamespace(t.disable),
	}

	status := &cobra.Command{
		Use:   "status",
		Short: "Show the tailnet topology and each exposed app's URL and state",
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
		Topology:     t.topology,
	}
	var warnings []string
	err := runShowingProgress("tailscale enable", func(progress docker.DeployProgressCallback) error {
		var err error
		warnings, err = ns.EnableTailscale(ctx, settings, progress)
		return err
	})
	printWarnings(warnings)
	if errors.Is(err, docker.ErrTagNotInPolicy) {
		// The requested topology decides which snippet is relevant; an
		// unset flag mirrors resolveTopology's own fresh-enable default so
		// the printed snippet matches what the failed enable just tried to
		// boot in the common case (no install yet).
		topology := t.topology
		if topology == "" {
			topology = docker.TailnetTopologyNodes
		}
		if facts, factsErr := docker.TailscaleTopologyFacts(topology); factsErr == nil {
			printPolicySnippet(facts, t.tag)
		}
	}
	var refused *docker.PolicyRefusedError
	if errors.As(err, &refused) {
		printMergedPolicy(refused.Review)
	}
	if err != nil {
		return err
	}

	fmt.Println("Tailscale enabled")
	return nil
}

// policy prints the tailnet policy snippet the requested topology needs;
// unlike enable/disable/status it touches no Docker namespace, so it needs
// no install to already exist.
func (t *tailscaleCommand) policy() error {
	facts, err := docker.TailscaleTopologyFacts(t.policyTopology)
	if err != nil {
		return err
	}
	printPolicySnippet(facts, t.tag)
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
	Topology     string                 `json:"topology,omitempty"`
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
	fmt.Println("Topology:", report.Topology)
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
		if errors.Is(err, docker.ErrOlderOnceSetup) {
			// Show the facade's own sentence verbatim, not stacked behind a
			// "reading Tailscale settings:" prefix that only restates where
			// it came from.
			return report, err
		}
		return report, fmt.Errorf("reading Tailscale settings: %w", err)
	}
	report.Topology = settings.Topology
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
			statuses[i].FunnelURL = settings.FunnelURL(s.Name, app.Settings.ActiveFunnelPort())
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

// printMergedPolicy hands a refused enable its fix (ADR-0017): the Merged
// Policy - the user's own tailnet policy plus the sections it is missing - to
// apply in the policy editor before re-running. Printing is the whole
// interaction: nothing prompts, so a script and a terminal behave the same.
func printMergedPolicy(review docker.PolicyReview) {
	fmt.Println()
	fmt.Printf("Apply this Merged Policy - your tailnet policy plus the missing %s - at https://login.tailscale.com/admin/acls, then re-run `once tailscale enable`:\n",
		strings.Join(review.Added, ", "))
	fmt.Println()
	fmt.Println(review.Merged)
	fmt.Println()
}

func printPolicySnippet(facts docker.TopologyFacts, tag string) {
	fmt.Println()
	fmt.Println("Merge this snippet into your tailnet policy file (https://login.tailscale.com/admin/acls), then run `once tailscale enable`:")
	fmt.Println()
	fmt.Println(facts.PolicySnippet(tag))
	fmt.Println()
}

// mustTailscaleTopologyFacts looks up topology's facts for a flag's
// construction-time default/help text; topology must be one of the two
// docker package constants, so the lookup can never actually fail.
func mustTailscaleTopologyFacts(topology string) docker.TopologyFacts {
	facts, err := docker.TailscaleTopologyFacts(topology)
	if err != nil {
		panic(err)
	}
	return facts
}
