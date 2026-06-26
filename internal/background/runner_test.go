package background

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunnerShutdown(t *testing.T) {
	runner := NewRunner("test")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := runner.Run(ctx)
	assert.NoError(t, err)
}

func TestSuperviseAdminServerRestartsAfterFailure(t *testing.T) {
	var calls atomic.Int32
	serving := make(chan struct{})
	run := func(ctx context.Context) error {
		if calls.Add(1) == 1 {
			return errors.New("listen race at boot")
		}
		close(serving) // second attempt succeeds and publishes the socket
		<-ctx.Done()
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { superviseAdminServer(ctx, run); close(done) }()

	select {
	case <-serving:
	case <-time.After(3 * time.Second):
		t.Fatal("server was not restarted after its first failure")
	}
	assert.GreaterOrEqual(t, calls.Load(), int32(2))

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not stop on context cancel")
	}
}

func TestSuperviseAdminServerStopsWithoutRestartOnCancel(t *testing.T) {
	var calls atomic.Int32
	run := func(ctx context.Context) error {
		calls.Add(1)
		<-ctx.Done()
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { superviseAdminServer(ctx, run); close(done) }()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not stop on context cancel")
	}
	require.Equal(t, int32(1), calls.Load())
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
