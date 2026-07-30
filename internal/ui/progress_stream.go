package ui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/basecamp/once/internal/docker"
)

// progressBuffer absorbs the frequent per-layer pull reports that arrive
// between two render frames.
const progressBuffer = 16

type progressUpdateMsg struct {
	progress docker.DeployProgress
}

// progressStream connects a background docker task to a Bubble Tea model: the
// task reports through a DeployProgressCallback, the model drains the reports
// with Wait, and the task's final message comes back as the Run command's
// result.
//
// A report is dropped rather than queued once the buffer is full, because a
// task can outlive its listener - the user cancels, or navigates on - and a
// progress bar is a lossy view of a monotonic percentage anyway. A blocking
// report would instead strand the task mid-deploy, with the old container
// already removed and the new one never started. Completion is never dropped:
// Bubble Tea delivers the Run command's result regardless of what the model
// is doing.
type progressStream struct {
	updates  chan docker.DeployProgress
	finished chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
}

func newProgressStream() *progressStream {
	ctx, cancel := context.WithCancel(context.Background())
	return &progressStream{
		updates:  make(chan docker.DeployProgress, progressBuffer),
		finished: make(chan struct{}),
		ctx:      ctx,
		cancel:   cancel,
	}
}

func (s *progressStream) Run(task func(context.Context, docker.DeployProgressCallback) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		defer s.cancel()
		defer close(s.finished)
		return task(s.ctx, s.report)
	}
}

// Wait yields the next report, or nil once the task has finished and every
// buffered report has been drained, which is how the model's drain loop ends.
func (s *progressStream) Wait() tea.Cmd {
	return func() tea.Msg {
		select {
		case p := <-s.updates:
			return progressUpdateMsg{progress: p}
		case <-s.finished:
			// Reports buffered before the task finished still belong on the
			// bar, and the outer select would pick between them at random.
			select {
			case p := <-s.updates:
				return progressUpdateMsg{progress: p}
			default:
				return nil
			}
		}
	}
}

func (s *progressStream) Cancel() {
	s.cancel()
}

// Private

func (s *progressStream) report(p docker.DeployProgress) {
	select {
	case s.updates <- p:
	default:
	}
}

// Helpers

// progressBarPercent maps a deploy stage onto the progress bar: only a
// download has a meaningful percentage, so every other stage falls back to the
// indeterminate animation.
func progressBarPercent(p docker.DeployProgress) int {
	if p.Stage == docker.DeployStageDownloading {
		return p.Percentage
	}
	return -1
}
