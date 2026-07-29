package docker

import (
	"context"
	"fmt"
)

// ApplySettings is the settings-change lifecycle shared by the CLI (`once
// update`) and the TUI settings forms: validate the new Configuration, reject
// a Host already used by another Application, set up the namespace, run the
// warn-only shadow and DNS checks, swap the Configuration in, and roll the
// container to pick it up - restoring the previous Configuration if that
// fails. A change that alters nothing short-circuits without rolling the
// container. The returned warnings never block the change.
//
// The roll goes through Deploy rather than Roll because the Configuration may
// carry a new image ref (`once update --image`), which must be pulled.
func (n *Namespace) ApplySettings(ctx context.Context, app *Application, settings ApplicationSettings, progress DeployProgressCallback) ([]string, error) {
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	if settings.Equal(app.Settings) {
		return nil, nil
	}
	if n.HostInUseByAnother(settings.Host, app.Settings.Name) {
		return nil, ErrHostnameInUse
	}
	if err := n.Setup(ctx); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSetupFailed, err)
	}

	var warnings []string
	if settings.Exposure == ExposureTailnet {
		warnings = append(warnings, n.tailscale.ShadowWarnings(ctx, []string{settings.Host})...)
	}
	if w := PublicDNSWarning(ctx, settings.Exposure, settings.Host); w != "" {
		warnings = append(warnings, w)
	}

	return warnings, rollConfiguration(ctx, app, settings, progress)
}

// Helpers

// rollConfiguration swaps the new Configuration in and rolls the container to
// pick it up, restoring the previous Configuration if the roll fails.
func rollConfiguration(ctx context.Context, app *Application, settings ApplicationSettings, progress DeployProgressCallback) error {
	previous := app.Settings
	app.Settings = settings
	if err := app.Deploy(ctx, progress); err != nil {
		app.Settings = previous
		return fmt.Errorf("%w: %w", ErrDeployFailed, err)
	}
	return nil
}
