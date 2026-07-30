package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/once/internal/docker"
)

type SettingsFormFunnel struct {
	settingsFormBase
}

// NewSettingsFormFunnel builds the Funnel form. tailscaleEnabled only drives
// the status line rendered at build time; the submit path checks live inside
// Namespace.EnableFunnel.
func NewSettingsFormFunnel(ns *docker.Namespace, app *docker.Application, tailscaleEnabled bool) SettingsFormFunnel {
	enableField := NewCheckboxField("Expose publicly via Funnel", app.Settings.FunnelEnabled())
	durationField := NewTextField("10m")
	durationField.SetValue(durationFromExpiry(app.Settings.FunnelExpiresAt, time.Now()))

	m := SettingsFormFunnel{
		settingsFormBase: settingsFormBase{
			title: "Tailscale Funnel",
			form: NewForm("Save",
				FormItem{Key: "enable", Label: "Funnel", Field: enableField},
				FormItem{Key: "duration", Label: "Duration (max 24h)", Field: durationField},
			),
		},
	}

	switch {
	case !tailscaleEnabled:
		m.statusLine = func() string {
			return "Tailscale is not enabled globally; enable it (press t on the dashboard) before using Funnel."
		}
	case app.Settings.FunnelEnabled():
		expiresAt := app.Settings.FunnelExpiresAt
		funnelURL := funnelURLForApp(ns.Tailscale(), app)
		m.statusLine = func() string {
			status := funnelStatusText(expiresAt, time.Now())
			if funnelURL != "" {
				status += " - " + funnelURL
			}
			return status
		}
	}

	m.form.OnSubmit(func(f *Form) tea.Cmd {
		enable := f.CheckboxField("enable").Checked()
		duration, err := parseFunnelDuration(f.TextField("duration").Value())
		// Only an enable spends the duration, so a disable is never held back by it.
		if enable && err != nil {
			f.SetError("duration", docker.ErrorMessage(err))
			return nil
		}
		return func() tea.Msg {
			return settingsRunActionMsg{action: func(_ docker.DeployProgressCallback) (string, error) {
				return applyFunnel(ns, app, enable, duration)
			}}
		}
	})
	m.form.OnCancel(func(f *Form) tea.Cmd {
		return func() tea.Msg { return SettingsSectionCancelMsg{} }
	})

	return m
}

func (m SettingsFormFunnel) Update(msg tea.Msg) (SettingsSection, tea.Cmd) {
	var cmd tea.Cmd
	m.settingsFormBase, cmd = m.update(msg)
	return m, cmd
}

// Helpers

func applyFunnel(ns *docker.Namespace, app *docker.Application, enable bool, duration time.Duration) (string, error) {
	ctx := context.Background()

	if !enable {
		if !app.Settings.FunnelEnabled() {
			return "Funnel is already inactive", nil
		}
		if err := app.DisableFunnel(ctx); err != nil {
			return "", err
		}
		return "Funnel disabled", nil
	}

	url, _, err := ns.EnableFunnel(ctx, app, duration)
	if err != nil {
		return "", funnelUserError(err)
	}
	return "Funnel enabled at " + url, nil
}

// funnelUserError swaps the engine's Tailscale-not-enabled sentinel for
// wording a TUI user can act on.
func funnelUserError(err error) error {
	if errors.Is(err, docker.ErrTailscaleNotEnabled) {
		return errors.New("enable Tailscale first (press t on the dashboard) before opening a Funnel")
	}
	return err
}

// funnelURLForApp returns the app's public Funnel URL from stored settings, or
// "" if the node hostname isn't known yet (no Funnel has activated).
func funnelURLForApp(ts *docker.Tailscale, app *docker.Application) string {
	settings, err := ts.LoadSettings(context.Background())
	if err != nil {
		return ""
	}
	return settings.FunnelURL(app.Settings.ActiveFunnelPort())
}

// parseFunnelDuration applies the bounds the Funnel spine applies, so a value
// that spine would reject is caught at the form instead of after the action
// has started running.
func parseFunnelDuration(value string) (time.Duration, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, errors.New("duration is required (e.g. 10m, 2h)")
	}
	duration, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q (e.g. 10m, 2h)", trimmed)
	}
	if err := docker.ValidateFunnelDuration(duration); err != nil {
		return 0, err
	}
	return duration, nil
}

func durationFromExpiry(expiresAt *time.Time, now time.Time) string {
	if expiresAt == nil || !expiresAt.After(now) {
		return "10m"
	}
	return formatDuration(expiresAt.Sub(now))
}
