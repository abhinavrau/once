package command

import (
	"context"
	"fmt"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/basecamp/once/internal/docker"
)

var hostStyle = lipgloss.NewStyle().Foreground(lipgloss.BrightBlue)

type listCommand struct {
	cmd  *cobra.Command
	json bool
}

func newListCommand() *listCommand {
	l := &listCommand{}
	l.cmd = &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List installed applications",
		RunE:    WithNamespace(l.run),
	}
	l.cmd.Flags().BoolVar(&l.json, "json", false, "emit structured JSON instead of prose")
	return l
}

// Private

func (l *listCommand) run(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
	apps := ns.Applications()
	tailnet := tailnetURLs(ctx, ns)

	if l.json {
		reports := make([]appReport, 0, len(apps))
		for _, app := range apps {
			reports = append(reports, newAppReport(app, tailnet[app.Settings.Name]))
		}
		return printJSON(reports)
	}

	printAppLines(ctx, apps, tailnet)
	return nil
}

// Helpers

// appReport is the stable JSON shape of one application, shared by
// `list --json` and `status --json`.
type appReport struct {
	Name            string          `json:"name"`
	Host            string          `json:"host"`
	Exposure        docker.Exposure `json:"exposure"`
	Running         bool            `json:"running"`
	URL             string          `json:"url,omitempty"`
	TailnetURL      string          `json:"tailnetUrl,omitempty"`
	FunnelExpiresAt *time.Time      `json:"funnelExpiresAt,omitempty"`
}

func newAppReport(app *docker.Application, tailnetURL string) appReport {
	return appReport{
		Name:            app.Settings.Name,
		Host:            app.Settings.Host,
		Exposure:        app.Settings.Exposure,
		Running:         app.Running,
		URL:             app.URL(),
		TailnetURL:      tailnetURL,
		FunnelExpiresAt: app.Settings.FunnelExpiresAt,
	}
}

// printAppLines prints each application's line, followed by the warn-only note
// when its tailnet service name does not resolve from this machine.
func printAppLines(ctx context.Context, apps []*docker.Application, tailnet map[string]string) {
	warnings := tailnetDNSWarnings(ctx, resolvableTailnetURLs(apps, tailnet))

	for i, app := range apps {
		fmt.Println(appLine(app, tailnet[app.Settings.Name]))
		if warnings[i] != "" {
			printWarnings([]string{warnings[i]})
		}
	}
}

// resolvableTailnetURLs gives each application's service URL, empty where there
// is nothing to resolve: a stopped app's service is deregistered, and apps that
// are not tailnet-exposed have no service at all.
func resolvableTailnetURLs(apps []*docker.Application, tailnet map[string]string) []string {
	urls := make([]string, len(apps))
	for i, app := range apps {
		if app.Running && app.Settings.Exposure == docker.ExposureTailnet {
			urls[i] = tailnet[app.Settings.Name]
		}
	}
	return urls
}

// tailnetDNSWarnings returns each URL's warn-only note, empty where the URL
// resolves or was skipped. The lookups run up front and in parallel, so a
// wedged resolver costs the command one timeout rather than one per row.
func tailnetDNSWarnings(ctx context.Context, serviceURLs []string) []string {
	warnings := make([]string, len(serviceURLs))
	var wg sync.WaitGroup
	for i, url := range serviceURLs {
		wg.Go(func() {
			warnings[i] = docker.TailnetDNSWarning(ctx, url)
		})
	}
	wg.Wait()
	return warnings
}

func appLine(app *docker.Application, tailnetURL string) string {
	status := "stopped"
	if app.Running {
		status = "running"
	}

	// Tailnet apps have no proxy URL; their host links to the service URL.
	url := app.URL()
	if url == "" {
		url = tailnetURL
	}
	host := hostStyle.Hyperlink(url).Render(app.Settings.Host)

	line := fmt.Sprintf("%s (%s)", host, status)
	if tailnetURL != "" {
		line += " " + hostStyle.Hyperlink(tailnetURL).Render(tailnetURL)
	}
	return line
}

// tailnetURLs maps app name to its tailnet service URL, computed from the
// Magic DNS suffix stored at enable time - no live lookup. Best effort: any
// failure returns nil so list still prints the public/local lines.
func tailnetURLs(ctx context.Context, ns *docker.Namespace) map[string]string {
	enabled, err := ns.Tailscale().Enabled(ctx)
	if err != nil || !enabled {
		return nil
	}

	settings, err := ns.Tailscale().LoadSettings(ctx)
	if err != nil {
		return nil
	}

	urls := make(map[string]string, len(ns.Applications()))
	for _, app := range ns.Applications() {
		if app.Settings.Exposure != docker.ExposureTailnet {
			continue
		}
		urls[app.Settings.Name] = settings.TailnetURL(app.Settings.Host)
	}
	return urls
}
