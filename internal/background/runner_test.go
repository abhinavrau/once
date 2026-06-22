package background

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRunnerShutdown(t *testing.T) {
	runner := NewRunner("test")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := runner.Run(ctx)
	assert.NoError(t, err)
}

func TestCheckInterval(t *testing.T) {
	assert.Equal(t, 5*time.Minute, CheckInterval)
}

func TestWakeInterval(t *testing.T) {
	now := time.Now()

	assert.Equal(t, CheckInterval, wakeInterval(now, nil, CheckInterval))

	soon := now.Add(2 * time.Minute)
	assert.Equal(t, 2*time.Minute, wakeInterval(now, []time.Time{soon}, CheckInterval))

	later := now.Add(20 * time.Minute)
	assert.Equal(t, CheckInterval, wakeInterval(now, []time.Time{later}, CheckInterval))

	assert.Equal(t, 1*time.Minute,
		wakeInterval(now, []time.Time{later, now.Add(time.Minute), soon}, CheckInterval))

	past := now.Add(-time.Minute)
	assert.Equal(t, time.Duration(0), wakeInterval(now, []time.Time{past}, CheckInterval))
}
