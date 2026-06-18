package command

import (
	"context"
	"fmt"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/basecamp/once/internal/docker"
)

var hostStyle = lipgloss.NewStyle().Foreground(lipgloss.BrightBlue)

type listCommand struct {
	cmd *cobra.Command
}

func newListCommand() *listCommand {
	l := &listCommand{}
	l.cmd = &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List installed applications",
		RunE:    WithNamespace(l.run),
	}
	return l
}

// Private

func (l *listCommand) run(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
	tailnet := tailnetURLs(ctx, ns)

	for _, app := range ns.Applications() {
		status := "stopped"
		if app.Running {
			status = "running"
		}

		host := hostStyle.Hyperlink(app.URL()).Render(app.Settings.Host)

		line := fmt.Sprintf("%s (%s)", host, status)
		if url := tailnet[app.Settings.Name]; url != "" {
			line += " " + hostStyle.Hyperlink(url).Render(url)
		}
		fmt.Println(line)
	}

	return nil
}

// Helpers

// tailnetURLs maps app name to tailnet URL when Tailscale is enabled. Best
// effort: any failure returns nil so list still prints the public/local lines.
func tailnetURLs(ctx context.Context, ns *docker.Namespace) map[string]string {
	enabled, err := ns.Tailscale().Enabled(ctx)
	if err != nil || !enabled {
		return nil
	}

	proxies, err := ns.Tailscale().Proxies(ctx)
	if err != nil {
		return nil
	}

	urls := make(map[string]string, len(proxies))
	for _, p := range proxies {
		urls[p.Name] = p.URL
	}
	return urls
}
