package command

import (
	"context"
	"fmt"
	"time"

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
		Short: "Enable Tailscale and boot the tsdproxy container",
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
		Short: "Disable Tailscale and remove the tsdproxy container",
		RunE:  WithNamespace(t.disable),
	}

	status := &cobra.Command{
		Use:   "status",
		Short: "Show tailnet node FQDNs, status, and active Funnels",
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

func (t *tailscaleCommand) status(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
	enabled, err := ns.Tailscale().Enabled(ctx)
	if err != nil {
		return fmt.Errorf("checking Tailscale: %w", err)
	}
	if !enabled {
		fmt.Println("Tailscale is not enabled")
		return nil
	}

	proxies, err := ns.Tailscale().Proxies(ctx)
	if err != nil {
		return fmt.Errorf("querying Tailscale status: %w", err)
	}
	if len(proxies) == 0 {
		if msg, err := ns.Tailscale().RegistrationError(ctx, time.Time{}); err == nil && msg != "" {
			fmt.Println("No tailnet nodes registered.")
			fmt.Printf("once-tsdproxy is rejecting node registration:\n  %s\n", msg)
			fmt.Println("This is commonly a tag missing from your tailnet ACL tagOwners, or a revoked/expired credential. " +
				"Fix it (Access Controls → tagOwners) and re-run `once tailscale enable`.")
			return nil
		}
		fmt.Println("No tailnet nodes registered (the tailnet may still be spinning up, or no apps are deployed yet)")
		return nil
	}

	for _, p := range proxies {
		line := fmt.Sprintf("%s\t%s\t%s", p.Name, p.Status, p.URL)
		if p.Funnel {
			line += "\tFunnel: active"
		}
		fmt.Println(line)
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
	// attribute, which Once can't manage. Only report it active once tsdproxy
	// confirms it.
	if err := ns.Tailscale().WaitForFunnelActive(ctx, app.Settings.Name); err != nil {
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
