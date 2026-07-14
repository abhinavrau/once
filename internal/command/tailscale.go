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
	cmd          *cobra.Command
	clientID     string
	clientSecret string
	tag          string
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
		RunE:  WithNamespace(t.enable),
	}
	enable.Flags().StringVar(&t.clientID, "client-id", "", "Tailscale OAuth client ID")
	enable.Flags().StringVar(&t.clientSecret, "client-secret", "", "Tailscale OAuth client secret")
	enable.Flags().StringVar(&t.tag, "tag", "", "Tailscale tag the OAuth client owns (e.g. tag:once)")
	enable.MarkFlagRequired("client-id")
	enable.MarkFlagRequired("client-secret")
	enable.MarkFlagRequired("tag")

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

func (t *tailscaleCommand) enable(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
	settings := docker.TailscaleSettings{ClientID: t.clientID, ClientSecret: t.clientSecret, Tag: t.tag}
	offerPolicyProvisioning(ctx, t.tag)
	if err := ns.EnableTailscale(ctx, settings); err != nil {
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

// status lists once-admin and each exposed app with its service URL (from the
// Magic DNS suffix stored at enable time) and its advertised/approved state,
// read live from the local daemon and the Tailscale cloud API.
func (t *tailscaleCommand) status(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
	enabled, err := ns.Tailscale().Enabled(ctx)
	if err != nil {
		return fmt.Errorf("checking Tailscale: %w", err)
	}
	if !enabled {
		fmt.Println("Tailscale is not enabled")
		return nil
	}

	settings, err := ns.Tailscale().LoadSettings(ctx)
	if err != nil {
		return fmt.Errorf("reading Tailscale settings: %w", err)
	}
	if settings.DomainSuffix == "" {
		fmt.Println("The tailnet's Magic DNS suffix is not known yet (the tailnet may still be spinning up); re-run in a moment")
		return nil
	}

	names := []string{"once-admin"}
	var stopped []string
	for _, app := range ns.Applications() {
		if !app.Settings.TailscaleExposed() {
			continue
		}
		if app.Running {
			names = append(names, app.Settings.Name)
		} else {
			stopped = append(stopped, app.Settings.Name)
		}
	}

	statuses, err := ns.Tailscale().ServiceStatuses(ctx, names)
	if err != nil {
		return err
	}
	for _, s := range statuses {
		fmt.Printf("%s\t%s\t%s\n", s.Name, s.URL, s.State)
	}
	for _, name := range stopped {
		fmt.Printf("%s\t%s\t%s\n", name, settings.TailnetURL(name), "not running")
	}
	return nil
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
	// tailscaled confirms it.
	if err := ns.Tailscale().WaitForFunnelActive(ctx); err != nil {
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

// Helpers

// offerPolicyProvisioning offers to write the four tailnet policy sections
// Once needs via the Tailscale API, so the user never hand-edits HuJSON. It
// runs before the enable's credential probe on purpose: on a fresh tailnet the
// probe cannot pass until the policy defines the tag. Best effort by design:
// any decline or failure prints the copy-paste snippet and enable proceeds,
// with the credential probe as the enforcement. Skipped when stdin is not a
// terminal, and on the headscale control seam (no policy API there).
func offerPolicyProvisioning(ctx context.Context, tag string) {
	if docker.TailscaleControlSeamActive() {
		return
	}
	if !term.IsTerminal(os.Stdin.Fd()) {
		fmt.Println("Skipping tailnet policy setup (not a terminal); run `once tailscale enable` interactively to be offered it.")
		return
	}

	fmt.Println("Once needs four sections in your tailnet policy file: tagOwners, autoApprovers.services, a grant for the tagged Services, and the funnel nodeAttr.")
	if !promptYesNo("Apply them now via the Tailscale API?") {
		printPolicySnippet(tag)
		return
	}

	fmt.Println("Enter an OAuth client with the policy_file scope. It can rewrite your entire tailnet policy, so use a throwaway client: it is used once, never stored, and can be revoked afterwards.")
	clientID := promptLine("Policy OAuth client ID: ")
	clientSecret := promptSecret("Policy OAuth client secret: ")
	if clientID == "" || clientSecret == "" {
		printPolicySnippet(tag)
		return
	}

	update, err := docker.PlanPolicyUpdate(ctx, clientID, clientSecret, tag)
	if err != nil {
		fmt.Println("Could not plan the policy update:", err)
		printPolicySnippet(tag)
		return
	}
	if !update.Changed() {
		fmt.Println("The tailnet policy already contains everything Once needs; nothing to change.")
		return
	}

	fmt.Println()
	fmt.Print(update.Diff())
	fmt.Println()
	fmt.Println("Warning: if this tailnet's policy is managed via GitOps, the next pipeline sync will silently revert this write - decline and commit the snippet to your policy repo instead.")
	if !promptYesNo("Write this policy change?") {
		printPolicySnippet(tag)
		return
	}
	if err := update.Apply(ctx); err != nil {
		fmt.Println("The policy write failed:", err)
		printPolicySnippet(tag)
		return
	}
	fmt.Println("Tailnet policy updated.")
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

func promptLine(prompt string) string {
	fmt.Print(prompt)
	var line string
	_, _ = fmt.Scanln(&line)
	return strings.TrimSpace(line)
}

// promptSecret reads without echoing, so the policy credential never lands in
// the terminal scrollback.
func promptSecret(prompt string) string {
	fmt.Print(prompt)
	secret, err := term.ReadPassword(os.Stdin.Fd())
	fmt.Println()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(secret))
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
