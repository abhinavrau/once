package ui

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/once/internal/docker"
)

// A measured transfer drives the bar whichever way the bytes are going: a
// pull in, or the offsite backup leg out.
func TestProgressBarPercent_TransferStagesDriveThePercent(t *testing.T) {
	assert.Equal(t, 42, progressBarPercent(docker.DeployProgress{Stage: docker.DeployStageDownloading, Percentage: 42}))
	assert.Equal(t, 42, progressBarPercent(docker.DeployProgress{Stage: docker.DeployStageUploading, Percentage: 42, Status: "uploading offsite"}))
}

func TestProgressBarPercent_NonTransferStagesAreIndeterminate(t *testing.T) {
	const indeterminate = -1
	assert.Equal(t, indeterminate, progressBarPercent(docker.DeployProgress{Stage: docker.DeployStageStarting}))
	assert.Equal(t, indeterminate, progressBarPercent(docker.DeployProgress{Stage: docker.DeployStageWaiting, Status: "verifying"}))
	assert.Equal(t, indeterminate, progressBarPercent(docker.DeployProgress{Stage: docker.DeployStageFinished}))
}

// The wedge this helper exists to prevent: a cancelled deploy leaves nobody
// draining the buffer, and a blocking report would strand the docker goroutine
// with the old container already removed and the new one never started.
func TestProgressStream_ReportsDoNotBlockWithNoDrainerArmed(t *testing.T) {
	s := newProgressStream()

	reports := 0
	msg := runWithin(t, s.Run(func(_ context.Context, report docker.DeployProgressCallback) tea.Msg {
		for i := range progressBuffer * 3 {
			report(docker.DeployProgress{Stage: docker.DeployStageDownloading, Percentage: i})
			reports++
		}
		return settingsDeployFinishedMsg{}
	}))

	assert.Equal(t, progressBuffer*3, reports)
	assert.Equal(t, settingsDeployFinishedMsg{}, msg)
}

func TestProgressStream_WaitYieldsReportsThenEnds(t *testing.T) {
	s := newProgressStream()
	s.report(docker.DeployProgress{Stage: docker.DeployStageDownloading, Percentage: 42})

	assert.Equal(t,
		progressUpdateMsg{progress: docker.DeployProgress{Stage: docker.DeployStageDownloading, Percentage: 42}},
		runWithin(t, s.Wait()),
	)

	runWithin(t, s.Run(func(context.Context, docker.DeployProgressCallback) tea.Msg { return nil }))
	assert.Nil(t, runWithin(t, s.Wait()), "the wait loop ends once the task has finished")
}

func TestProgressStream_CancelStopsTheTaskContext(t *testing.T) {
	s := newProgressStream()
	s.Cancel()

	err := runWithin(t, s.Run(func(ctx context.Context, _ docker.DeployProgressCallback) tea.Msg {
		return ctx.Err()
	}))
	assert.Equal(t, context.Canceled, err)
}

// Helpers

// runWithin runs a command off the test goroutine so a command that wrongly
// blocks fails the test instead of hanging the suite.
func runWithin(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	require.NotNil(t, cmd)

	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()

	select {
	case msg := <-result:
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("command blocked")
		return nil
	}
}
