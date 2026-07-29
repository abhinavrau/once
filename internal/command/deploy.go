package command

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/basecamp/once/internal/catalog"
	"github.com/basecamp/once/internal/docker"
)

type deployCommand struct {
	cmd   *cobra.Command
	flags settingsFlags
}

func newDeployCommand() *deployCommand {
	d := &deployCommand{}
	d.cmd = &cobra.Command{
		Use:   "deploy <app or image>",
		Short: "Deploy an application",
		Args:  cobra.ExactArgs(1),
		RunE:  WithNamespace(d.run),
	}

	d.flags.register(d.cmd)

	return d
}

// Private

func (d *deployCommand) run(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
	imageRef := catalog.Resolve(args[0])

	if err := d.flags.checkRemoved(cmd); err != nil {
		return err
	}

	exposure, err := docker.ParseExposure(d.flags.expose)
	if err != nil {
		return err
	}

	host, err := docker.ResolveHost(exposure, d.flags.host, docker.NameFromImageRef(imageRef))
	if err != nil {
		return err
	}

	settings, err := d.flags.buildSettings(imageRef, host, exposure)
	if err != nil {
		return err
	}

	var warnings []string
	err = runWithProgress("Deploying "+host, func(progress docker.DeployProgressCallback) error {
		_, w, deployErr := ns.Deploy(ctx, settings, progress)
		warnings = w
		return deployErr
	})
	printWarnings(warnings)
	return hintTailscaleEnable(err)
}
