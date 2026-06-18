package command

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/once/internal/docker"
)

type tailscaleCommand struct {
	cmd          *cobra.Command
	clientID     string
	clientSecret string
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
	enable.MarkFlagRequired("client-id")
	enable.MarkFlagRequired("client-secret")

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

	t.cmd.AddCommand(enable, disable, status)
	return t
}

// Private

func (t *tailscaleCommand) enable(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
	if err := ns.EnsureNetwork(ctx); err != nil {
		return fmt.Errorf("ensuring network: %w", err)
	}

	settings := docker.TailscaleSettings{ClientID: t.clientID, ClientSecret: t.clientSecret}
	if err := ns.Tailscale().Enable(ctx, settings); err != nil {
		return fmt.Errorf("enabling Tailscale: %w", err)
	}

	fmt.Println("Tailscale enabled")
	return nil
}

func (t *tailscaleCommand) disable(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
	if err := ns.Tailscale().Disable(ctx); err != nil {
		return fmt.Errorf("disabling Tailscale: %w", err)
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
		fmt.Println("No tailnet nodes registered")
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
