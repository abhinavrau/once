package command

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/once/internal/docker"
)

type statusCommand struct {
	cmd  *cobra.Command
	json bool
}

func newStatusCommand() *statusCommand {
	s := &statusCommand{}
	s.cmd = &cobra.Command{
		Use:   "status <host>",
		Short: "Show an application's exposure and running state",
		Args:  cobra.ExactArgs(1),
		RunE:  WithNamespace(s.run),
	}
	s.cmd.Flags().BoolVar(&s.json, "json", false, "emit structured JSON instead of prose")
	return s
}

// Private

func (s *statusCommand) run(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
	app := ns.ApplicationByHost(args[0])
	if app == nil {
		return fmt.Errorf("no application found at host %q", args[0])
	}

	tailnet := tailnetURLs(ctx, ns)
	if s.json {
		return printJSON(newAppReport(app, tailnet[app.Settings.Name]))
	}

	printAppLines(ctx, []*docker.Application{app}, tailnet)
	return nil
}
