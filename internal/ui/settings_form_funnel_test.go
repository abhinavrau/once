package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
)

func TestFunnelStatusText(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)

	assert.Equal(t, "Inactive", funnelStatusText(nil, now))

	past := now.Add(-time.Minute)
	assert.Equal(t, "Inactive", funnelStatusText(&past, now))

	future := now.Add(9 * time.Minute)
	assert.Equal(t, "Active (Expires in 9m)", funnelStatusText(&future, now))
}

func TestDurationFromExpiry(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)

	assert.Equal(t, "10m", durationFromExpiry(nil, now))

	past := now.Add(-time.Minute)
	assert.Equal(t, "10m", durationFromExpiry(&past, now))

	future := now.Add(15 * time.Minute)
	assert.Equal(t, "15m", durationFromExpiry(&future, now))
}

func TestApplyFunnelDisableWhenInactive(t *testing.T) {
	app := &docker.Application{Settings: docker.ApplicationSettings{Name: "books"}}

	// ns is nil: the inactive-disable branch returns before touching it.
	msg, err := applyFunnel(nil, app, false, 0)
	require.NoError(t, err)
	assert.Equal(t, "Funnel is already inactive", msg)
}

func TestParseFunnelDuration(t *testing.T) {
	duration, err := parseFunnelDuration(" 90m ")
	require.NoError(t, err)
	assert.Equal(t, 90*time.Minute, duration)

	_, err = parseFunnelDuration("")
	assert.ErrorContains(t, err, "duration is required")

	_, err = parseFunnelDuration("nonsense")
	assert.ErrorContains(t, err, "invalid duration")

	_, err = parseFunnelDuration("0m")
	assert.ErrorIs(t, err, docker.ErrFunnelDurationInvalid)

	_, err = parseFunnelDuration("48h")
	assert.ErrorIs(t, err, docker.ErrFunnelDurationTooLong)
}

func TestFunnelUserErrorWordsTailscaleDisabledForTheTUI(t *testing.T) {
	err := funnelUserError(docker.ErrTailscaleNotEnabled)
	assert.ErrorContains(t, err, "enable Tailscale first")
	assert.ErrorContains(t, err, "press t on the dashboard")

	assert.Equal(t, assert.AnError, funnelUserError(assert.AnError))
}

func TestSettingsFormFunnelStatusLineWhenDisabled(t *testing.T) {
	app := &docker.Application{Settings: docker.ApplicationSettings{Name: "books"}}

	form := NewSettingsFormFunnel(nil, app, false)
	assert.Contains(t, form.StatusLine(), "Tailscale is not enabled")
}

func TestSettingsFormFunnel_EnableRejectsUnusableDuration(t *testing.T) {
	assertRejected := func(t *testing.T, duration, wantError string) {
		t.Helper()
		form := newFunnelForm(t, true, duration)
		cmd := funnelSubmit(&form)

		require.True(t, form.form.HasError())
		assert.Contains(t, form.form.Error(), wantError)
		assert.Equal(t, 1, form.form.Focused(), "refocuses the duration field")

		rejected, ok := findFormRejection(cmd)
		require.True(t, ok, "expected a FormSubmitRejectedMsg")
		assert.Equal(t, "duration", rejected.Key)
		assert.Equal(t, form.form.Error(), rejected.Reason)

		updateSettingsForm(&form, keyPressMsg("x"))
		assert.False(t, form.form.HasError(), "typing in the refocused field clears the inline error")
	}

	t.Run("empty", func(t *testing.T) { assertRejected(t, "", "duration is required") })
	t.Run("unparseable", func(t *testing.T) { assertRejected(t, "nonsense", "invalid duration") })
	t.Run("over the maximum", func(t *testing.T) { assertRejected(t, "48h", "must not exceed") })
}

func TestSettingsFormFunnel_EnableSubmitsValidDuration(t *testing.T) {
	form := newFunnelForm(t, true, "2h")
	cmd := funnelSubmit(&form)

	assert.False(t, form.form.HasError())
	require.NotNil(t, cmd)
	_, ok := cmd().(settingsRunActionMsg)
	assert.True(t, ok, "expected settingsRunActionMsg")
}

// Disabling never spends the duration, so it submits whatever the field holds.
func TestSettingsFormFunnel_DisableSubmitsWithoutDuration(t *testing.T) {
	form := newFunnelForm(t, false, "")
	cmd := funnelSubmit(&form)

	assert.False(t, form.form.HasError())
	require.NotNil(t, cmd)
	msg, ok := cmd().(settingsRunActionMsg)
	require.True(t, ok, "expected settingsRunActionMsg")

	// app has no active Funnel, so the action short-circuits without Docker.
	result, err := msg.action(nil)
	require.NoError(t, err)
	assert.Equal(t, "Funnel is already inactive", result)
}

// Helpers

// newFunnelForm builds the form for an app with no active Funnel, so the
// namespace is never consulted and can be nil.
func newFunnelForm(t *testing.T, enable bool, duration string) SettingsFormFunnel {
	t.Helper()

	app := &docker.Application{Settings: docker.ApplicationSettings{Name: "books"}}
	form := NewSettingsFormFunnel(nil, app, true)
	if enable {
		updateSettingsForm(&form, keyPressMsg("space"))
	}
	form.form.TextField("duration").SetValue(duration)
	return form
}

func funnelSubmit(form *SettingsFormFunnel) tea.Cmd {
	updateSettingsForm(form, keyPressMsg("tab")) // → duration
	updateSettingsForm(form, keyPressMsg("tab")) // → Save

	result, cmd := form.Update(keyPressMsg("enter"))
	*form = result.(SettingsFormFunnel)
	return cmd
}
