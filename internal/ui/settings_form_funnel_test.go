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

	// ts is nil: the inactive-disable branch returns before touching it.
	msg, err := applyFunnel(app, nil, "once", true, false, "")
	require.NoError(t, err)
	assert.Equal(t, "Funnel is already inactive", msg)
}

func TestApplyFunnelRejectsBadDuration(t *testing.T) {
	app := &docker.Application{Settings: docker.ApplicationSettings{Name: "books"}}

	// Both rejections happen before any docker/tailscale call, so ts is nil.
	_, err := applyFunnel(app, nil, "once", true, true, "nonsense")
	assert.ErrorContains(t, err, "invalid duration")

	_, err = applyFunnel(app, nil, "once", true, true, "48h")
	assert.Error(t, err) // exceeds 24h max, rejected before any side effect
}

func TestApplyFunnelRejectsWhenTailscaleDisabled(t *testing.T) {
	app := &docker.Application{Settings: docker.ApplicationSettings{Name: "books"}}

	_, err := applyFunnel(app, nil, "once", false, true, "10m")
	assert.ErrorContains(t, err, "enable Tailscale first")
}

func TestSettingsFormFunnelStatusLineWhenDisabled(t *testing.T) {
	app := &docker.Application{Settings: docker.ApplicationSettings{Name: "books"}}

	form := NewSettingsFormFunnel(app, nil, "once", false)
	assert.Contains(t, form.StatusLine(), "Tailscale is not enabled")
}
