package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/once/internal/docker"
	"github.com/basecamp/once/internal/service"
)

const (
	funnelEnableField = iota
	funnelDurationField
)

// backgroundServiceSuffix mirrors the command package's daemon naming. Funnel
// teardown on expiry runs in that daemon, so enabling a Funnel without it would
// leave the app public indefinitely.
const backgroundServiceSuffix = "-background"

type SettingsFormFunnel struct {
	settingsFormBase
}

func NewSettingsFormFunnel(app *docker.Application, ts *docker.Tailscale, namespace string, tailscaleEnabled bool) SettingsFormFunnel {
	enableField := NewCheckboxField("Expose publicly via Funnel", app.Settings.FunnelEnabled())
	durationField := NewTextField("10m")
	durationField.SetValue(durationFromExpiry(app.Settings.FunnelExpiresAt, time.Now()))

	m := SettingsFormFunnel{
		settingsFormBase: settingsFormBase{
			title: "Tailscale Funnel",
			form: NewForm("Save",
				FormItem{Label: "Funnel", Field: enableField},
				FormItem{Label: "Duration (max 24h)", Field: durationField},
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
		m.statusLine = func() string {
			return funnelStatusText(expiresAt, time.Now())
		}
	}

	m.form.OnSubmit(func(f *Form) tea.Cmd {
		enable := f.CheckboxField(funnelEnableField).Checked()
		duration := f.TextField(funnelDurationField).Value()
		return func() tea.Msg {
			return settingsRunActionMsg{action: func() (string, error) {
				return applyFunnel(app, ts, namespace, tailscaleEnabled, enable, duration)
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

func applyFunnel(app *docker.Application, ts *docker.Tailscale, namespace string, tailscaleEnabled, enable bool, durationStr string) (string, error) {
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

	if !tailscaleEnabled {
		return "", fmt.Errorf("enable Tailscale first (press t on the dashboard) before opening a Funnel")
	}

	duration, err := time.ParseDuration(strings.TrimSpace(durationStr))
	if err != nil {
		return "", fmt.Errorf("invalid duration %q (e.g. 10m, 2h)", durationStr)
	}
	if err := docker.ValidateFunnelDuration(duration); err != nil {
		return "", err
	}
	// Daemon required: without it the automatic teardown at expiry could never
	// run and the app would stay public indefinitely.
	if err := requireBackgroundDaemon(namespace); err != nil {
		return "", err
	}
	if err := app.EnableFunnel(ctx, time.Now().Add(duration)); err != nil {
		return "", err
	}
	// Surface activation failures (e.g. a tailnet ACL missing the funnel
	// attribute) rather than reporting the Funnel active when it isn't.
	if err := ts.WaitForFunnelActive(ctx, app.Settings.Name); err != nil {
		return "", err
	}
	return "Funnel enabled", nil
}

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

func durationFromExpiry(expiresAt *time.Time, now time.Time) string {
	if expiresAt == nil || !expiresAt.After(now) {
		return "10m"
	}
	return formatDuration(expiresAt.Sub(now))
}
