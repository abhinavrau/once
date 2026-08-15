package command

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/once/internal/docker"
	"github.com/basecamp/once/internal/ui"
)

type cliProgress struct {
	label        string
	stage        string
	progress     ui.Progress
	progressChan chan docker.DeployProgress
	err          error
	width        int

	task func(docker.DeployProgressCallback) error
}

type (
	cliProgressDoneMsg   struct{ err error }
	cliProgressUpdateMsg struct{ p docker.DeployProgress }
)

func newCLIProgress(label string, task func(docker.DeployProgressCallback) error) *cliProgress {
	return &cliProgress{
		label:        label,
		stage:        "preparing",
		progress:     ui.NewProgress(0, lipgloss.BrightBlue),
		progressChan: make(chan docker.DeployProgress, 16),
		task:         task,
	}
}

func runWithProgress(label string, task func(docker.DeployProgressCallback) error) error {
	err := runShowingProgress(label, task)

	if err != nil {
		fmt.Printf("%s: %s\n", label, lipgloss.NewStyle().Foreground(lipgloss.Red).Render("failed"))
	} else {
		fmt.Printf("%s: %s\n", label, lipgloss.NewStyle().Foreground(lipgloss.Green).Render("done"))
	}

	return err
}

// runShowingProgress renders progress with a spinner on a terminal; when
// stdout is piped it prints waiting statuses as plain lines, so automated
// callers see progress without ANSI frames.
func runShowingProgress(label string, task func(docker.DeployProgressCallback) error) error {
	if isTerminal() {
		p := newCLIProgress(label, task)
		if _, runErr := tea.NewProgram(p).Run(); runErr != nil {
			return runErr
		}
		return p.err
	}
	return task(func(p docker.DeployProgress) {
		if p.Stage == docker.DeployStageWaiting {
			fmt.Println(p.Status)
		}
	})
}

func (m *cliProgress) Init() tea.Cmd {
	return tea.Batch(
		m.runTask(),
		m.waitForProgress(),
		m.progress.Init(),
	)
}

func (m *cliProgress) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.progress = m.progress.SetWidth(m.barWidth())
		return m, nil

	case cliProgressUpdateMsg:
		switch msg.p.Stage {
		case docker.DeployStageDownloading:
			m.stage = "downloading"
			m.progress = m.progress.SetPercent(msg.p.Percentage)
		case docker.DeployStageStarting:
			m.stage = "starting"
			m.progress = m.progress.SetPercent(-1)
		case docker.DeployStageWaiting:
			m.stage = msg.p.Status
			m.progress = m.progress.SetPercent(-1)
		case docker.DeployStageUploading:
			// A measured send: the status names what is going out, the bar
			// says how far. "downloading" would be the wrong word entirely.
			m.stage = msg.p.Status
			m.progress = m.progress.SetPercent(msg.p.Percentage)
		}
		return m, m.waitForProgress()

	case cliProgressDoneMsg:
		m.err = msg.err
		return m, tea.Quit

	case ui.ProgressTickMsg:
		var cmd tea.Cmd
		m.progress, cmd = m.progress.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m *cliProgress) View() tea.View {
	prefix := m.label + " " + m.stage + ": "
	return tea.NewView(prefix + m.progress.View())
}

// Private

func (m *cliProgress) barWidth() int {
	prefixWidth := len(m.label) + 1 + len(m.stage) + 2 // " " + stage + ": "
	w := max(m.width-prefixWidth, 10)
	return w
}

func (m *cliProgress) runTask() tea.Cmd {
	return func() tea.Msg {
		callback := func(p docker.DeployProgress) {
			m.progressChan <- p
		}
		err := m.task(callback)
		return cliProgressDoneMsg{err: err}
	}
}

func (m *cliProgress) waitForProgress() tea.Cmd {
	return func() tea.Msg {
		p := <-m.progressChan
		return cliProgressUpdateMsg{p: p}
	}
}

// Helpers

func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
