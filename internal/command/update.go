package command

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basecamp/once/internal/docker"
)

type updateCommand struct {
	cmd   *cobra.Command
	flags settingsFlags
	image string
}

func newUpdateCommand() *updateCommand {
	u := &updateCommand{}
	u.cmd = &cobra.Command{
		Use:   "update <host>",
		Short: "Update settings for a deployed application",
		Args:  cobra.ExactArgs(1),
		RunE:  WithNamespace(u.run),
	}

	u.flags.register(u.cmd)
	u.cmd.Flags().StringVar(&u.image, "image", "", "new image for the application")

	return u
}

// Private

func (u *updateCommand) run(ctx context.Context, ns *docker.Namespace, cmd *cobra.Command, args []string) error {
	currentHost := args[0]

	if err := u.flags.checkRemoved(cmd); err != nil {
		return err
	}

	app := ns.ApplicationByHost(currentHost)
	if app == nil {
		return fmt.Errorf("no application found at host %q", currentHost)
	}

	image := app.Settings.Image
	if cmd.Flags().Changed("image") {
		image = u.image
	}

	settings, err := u.flags.applyChanges(cmd, app.Settings, image)
	if err != nil {
		return err
	}

	if err := ns.Setup(ctx); err != nil {
		return fmt.Errorf("%w: %w", docker.ErrSetupFailed, err)
	}

	if settings.Host != app.Settings.Host {
		if ns.HostInUseByAnother(settings.Host, app.Settings.Name) {
			return docker.ErrHostnameInUse
		}
	}

	warnShadowedServiceName(ctx, ns, settings)

	oldSettings := app.Settings
	app.Settings = settings

	return runWithProgress("Updating "+currentHost, func(progress docker.DeployProgressCallback) error {
		if err := app.Deploy(ctx, progress); err != nil {
			app.Settings = oldSettings
			return fmt.Errorf("%w: %w", docker.ErrDeployFailed, err)
		}
		return nil
	})
}
