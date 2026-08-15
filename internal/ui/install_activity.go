package ui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/once/internal/catalog"
	"github.com/basecamp/once/internal/docker"
)

type installStage int

const (
	stagePreparing installStage = iota
	stageDownloading
	stageStarting
	stageVerifying
)

type installDoneMsg struct {
	app      *docker.Application
	warnings []string
	err      error
}

type InstallActivityDoneMsg struct {
	App      *docker.Application
	Warnings []string
}

type InstallActivityFailedMsg struct {
	Err error
}

type InstallActivity struct {
	namespace     *docker.Namespace
	app           catalog.App
	hostname      string
	exposure      docker.Exposure
	autoBackup    bool
	width, height int
	stage         installStage
	progress      Progress
	stream        *progressStream
}

func NewInstallActivity(ns *docker.Namespace, app catalog.App, hostname string, exposure docker.Exposure, autoBackup bool) *InstallActivity {
	return &InstallActivity{
		namespace:  ns,
		app:        app,
		hostname:   hostname,
		exposure:   exposure,
		autoBackup: autoBackup,
		stage:      stagePreparing,
		progress:   NewProgress(0, Colors.Primary),
		stream:     newProgressStream(),
	}
}

func (m *InstallActivity) Init() tea.Cmd {
	return tea.Batch(m.progress.Init(), m.stream.Run(m.runInstall), m.stream.Wait())
}

func (m *InstallActivity) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.progress = m.progress.SetWidth(min(m.width-4, 60))

	case progressUpdateMsg:
		m.stage = installStageFor(msg.progress.Stage, m.stage)
		m.progress = m.progress.SetPercent(progressBarPercent(msg.progress))
		return m.stream.Wait()

	case installDoneMsg:
		if msg.err != nil {
			return func() tea.Msg { return InstallActivityFailedMsg{Err: msg.err} }
		}
		return func() tea.Msg { return InstallActivityDoneMsg{App: msg.app, Warnings: msg.warnings} }

	case ProgressTickMsg:
		var cmd tea.Cmd
		m.progress, cmd = m.progress.Update(msg)
		return cmd
	}

	return nil
}

func (m *InstallActivity) View() string {
	var status string
	switch m.stage {
	case stagePreparing:
		status = "Preparing..."
	case stageDownloading:
		status = "Downloading..."
	case stageStarting:
		status = "Starting..."
	case stageVerifying:
		status = "Verifying..."
	}

	statusLine := Styles.CenteredLine(m.width, status)

	progressView := Styles.CenteredLine(m.width, m.progress.View())

	return lipgloss.JoinVertical(lipgloss.Left, statusLine, progressView)
}

func (m *InstallActivity) Cancel() {
	m.stream.Cancel()
}

// Private

func (m *InstallActivity) runInstall(ctx context.Context, progress docker.DeployProgressCallback) (msg tea.Msg) {
	defer func() {
		if r := recover(); r != nil {
			msg = installDoneMsg{err: fmt.Errorf("install panicked: %v", r)}
		}
	}()

	app, warnings, err := m.namespace.Deploy(ctx, installSettings(m.app, m.hostname, m.exposure, m.autoBackup), progress)
	if err != nil {
		return installDoneMsg{err: err}
	}
	return installDoneMsg{app: app, warnings: warnings}
}

// Helpers

// installStageFor maps a deploy stage onto the install activity's status text,
// keeping the current stage for stages it does not narrate.
func installStageFor(stage docker.DeployStage, current installStage) installStage {
	switch stage {
	case docker.DeployStageDownloading:
		return stageDownloading
	case docker.DeployStageStarting:
		return stageStarting
	case docker.DeployStageWaiting:
		return stageVerifying
	}
	return current
}

// installSettings builds a freshly installed app's settings from the shared
// defaults, so TUI installs and flagless CLI deploys start life identically.
// The deploy spine assigns the app's unique name.
func installSettings(app catalog.App, host string, exposure docker.Exposure, autoBackup bool) docker.ApplicationSettings {
	s := docker.DefaultApplicationSettings()
	s.Image = app.ImageRef
	s.Host = host
	s.Exposure = exposure
	s.Profile = app.Profile
	s.Backup.AutoBackup = autoBackup
	return s
}
