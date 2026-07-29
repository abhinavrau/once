package ui

import (
	"testing"
	"time"

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
	msg, err := applyFunnel(nil, app, false, "")
	require.NoError(t, err)
	assert.Equal(t, "Funnel is already inactive", msg)
}

func TestApplyFunnelRejectsBadDuration(t *testing.T) {
	app := &docker.Application{Settings: docker.ApplicationSettings{Name: "books"}}

	// An unparseable duration is rejected before the spine runs, so ns is nil.
	_, err := applyFunnel(nil, app, true, "nonsense")
	assert.ErrorContains(t, err, "invalid duration")

	// An out-of-bounds duration is rejected by the spine's validation before
	// any Docker or service-manager call.
	ns, err := docker.NewNamespace("once")
	require.NoError(t, err)
	_, err = applyFunnel(ns, app, true, "48h")
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
