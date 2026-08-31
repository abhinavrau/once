package command

import (
	"context"
	"errors"
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
	report, err := tailscaleSettingsForReport(ctx, ns)
	if err != nil && !l.json {
		printWarnings([]string{docker.ErrorMessage(err)})
	}

	if l.json {
		reports := make([]appReport, 0, len(apps))
		for _, app := range apps {
			reports = append(reports, newAppReport(app, tailnetURLFor(app, report), funnelURLFor(app, report)))
		}
		return printJSON(reports)
	}

	printAppLines(ctx, apps, report)
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
	FunnelURL       string          `json:"funnelUrl,omitempty"`
	FunnelExpiresAt *time.Time      `json:"funnelExpiresAt,omitempty"`
}

func newAppReport(app *docker.Application, tailnetURL, funnelURL string) appReport {
	return appReport{
		Name:            app.Settings.Name,
		Host:            app.Settings.Host,
		Exposure:        app.Settings.Exposure,
		Running:         app.Running,
		URL:             app.URL(),
		TailnetURL:      tailnetURL,
		FunnelURL:       funnelURL,
		FunnelExpiresAt: app.Settings.FunnelExpiresAt,
	}
}

// printAppLines prints each application's line, followed by the warn-only note
// when its tailnet service name does not resolve from this machine.
func printAppLines(ctx context.Context, apps []*docker.Application, report tailnetReport) {
	tailnet := make(map[string]string, len(apps))
	for _, app := range apps {
		tailnet[app.Settings.Name] = tailnetURLFor(app, report)
	}
	warnings := tailnetDNSWarnings(ctx, resolvableTailnetURLs(apps, tailnet))

	for i, app := range apps {
		fmt.Println(appLine(app, tailnet[app.Settings.Name]))
		if funnelURL := funnelURLFor(app, report); funnelURL != "" {
			fmt.Printf("\tFunnel: %s\n", funnelURL)
		}
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

// tailnetReport is everything list's and status's per-app tailnet and Funnel
// URLs are rendered from, read once per command rather than reconstructed per
// app: the facade's settings, whether they are usable at all, and the live
// FQDNs the apps' nodes answer on. The FQDNs are what keep a printed URL
// dialable - under nodes an app's node can hold a name Tailscale deduped
// (campfire-1), which the settings alone cannot know.
type tailnetReport struct {
	settings docker.TailscaleSettings
	enabled  bool
	fqdns    map[string]string
}

// tailscaleSettingsForReport reads the facade once for list's and status's
// per-app tailnet and Funnel URLs, so both are rendered from one facade read
// rather than reconstructed by this command; enabled is false when Tailscale
// is not enabled, or its settings could not be read. Best effort in the
// general case (any other read failure degrades to disabled so these commands
// still print the public/local lines), except the one failure worth telling
// the user about: a stored label with no topology, whose message the caller
// shows once, verbatim.
func tailscaleSettingsForReport(ctx context.Context, ns *docker.Namespace) (tailnetReport, error) {
	enabled, enabledErr := ns.Tailscale().Enabled(ctx)
	if enabledErr != nil || !enabled {
		return tailnetReport{}, nil
	}

	settings, err := ns.Tailscale().LoadSettings(ctx)
	if err != nil {
		if errors.Is(err, docker.ErrOlderOnceSetup) {
			return tailnetReport{}, err
		}
		return tailnetReport{}, nil
	}
	fqdns, _ := ns.Tailscale().NodeFQDNs(ctx)
	return tailnetReport{settings: settings, enabled: true, fqdns: fqdns}, nil
}

// tailnetURLFor gives app's tailnet service URL, or "" when Tailscale is
// disabled or the app has no tailnet service at all.
func tailnetURLFor(app *docker.Application, report tailnetReport) string {
	if !report.enabled || app.Settings.Exposure != docker.ExposureTailnet {
		return ""
	}
	return report.settings.TailnetURLFor(app.Settings.Host, report.fqdns)
}

// funnelURLFor gives app's public Funnel URL, or "" when Tailscale is
// disabled, the app has no tailnet service, or its Funnel is not active.
func funnelURLFor(app *docker.Application, report tailnetReport) string {
	if !report.enabled || app.Settings.Exposure != docker.ExposureTailnet {
		return ""
	}
	return report.settings.FunnelURL(app.Settings.Host, app.Settings.ActiveFunnelPort(), report.fqdns)
}
