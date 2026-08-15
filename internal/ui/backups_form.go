package ui

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/once/internal/docker"
)

const backupsFormTextWidth = 72

var backupsFormCloseKey = WithHelp(NewKeyBinding("esc"), "esc", "close")

type BackupsFormCloseMsg struct{}

type backupsEnableSubmitMsg struct {
	settings docker.BackupRepoSettings
}

type backupsDisableSubmitMsg struct{}

type backupsProbeFinishedMsg struct {
	settings docker.BackupRepoSettings
	err      error
}

type backupsEnabledMsg struct {
	err error
}

// BackupsForm is the dashboard-global overlay for the install-wide Backup
// Repo: local path, offsite URL, credential name/value pairs. Submitting runs
// the enable sequence - persist, validation probe, custody flow, Enabled -
// with the custody moment rendered inside the overlay.
type BackupsForm struct {
	namespace     *docker.Namespace
	enabled       bool
	form          Form
	help          Help
	width, height int
	running       bool
	status        string
	// detail is the live step under the status line: which repository is being
	// reached, and - for the offsite upload, the one step with a person
	// waiting on a network - what restic's own progress stream says.
	detail   string
	progress Progress
	// stream pumps the probe's progress reports back onto the model. One per
	// run: it closes when the task finishes.
	stream  *progressStream
	err     error
	custody *Custody
	handoff CustodyHandoff
}

func NewBackupsForm(ns *docker.Namespace) BackupsForm {
	return newBackupsForm(ns, ns.BackupRepoSettings())
}

func (m BackupsForm) Init() tea.Cmd {
	return m.form.Init()
}

// Busy guards the quit key while the phone handoff is published. The route and
// certificate it puts in the Proxy outlive this process, and only the custody
// flow knows how to take them down, so quitting out from under it strands them.
func (m BackupsForm) Busy() bool {
	return m.custody != nil && m.custody.HandoffLive()
}

// ShowingSecret propagates the custody screen's mouse-tracking opt-out.
func (m BackupsForm) ShowingSecret() bool {
	return m.custody != nil && m.custody.ShowingSecret()
}

func (m BackupsForm) Update(msg tea.Msg) (Component, tea.Cmd) {
	if wsm, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = wsm.Width, wsm.Height
		m.help.SetWidth(m.width)
		m.progress = m.progress.SetWidth(m.width)
		m.form, _ = m.form.Update(msg)
		if m.custody != nil {
			c, _ := m.custody.Update(msg)
			m.custody = &c
		}
		return m, nil
	}

	// The custody moment owns the overlay once it starts: every key belongs
	// to it, including esc (its skip hatch), never to the close binding.
	if m.custody != nil {
		return m.updateCustody(msg)
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if m.running {
			return m, nil
		}
		if key.Matches(msg, backupsFormCloseKey) {
			return m, func() tea.Msg { return BackupsFormCloseMsg{} }
		}
		m.err = nil

	case MouseEvent:
		if m.running {
			return m, nil
		}

	case backupsEnableSubmitMsg:
		m.stream = newProgressStream()
		return m.startRunning("Validating the backup repositories...", tea.Batch(m.runProbe(msg.settings), m.stream.Wait()))

	case progressUpdateMsg:
		if !m.running || m.stream == nil {
			return m, nil
		}
		m.detail = msg.progress.Status
		m.progress = m.progress.SetPercent(progressBarPercent(msg.progress))
		return m, m.stream.Wait()

	case backupsDisableSubmitMsg:
		return m.startRunning("Saving...", func() tea.Msg {
			settings := m.namespace.BackupRepoSettings()
			settings.Enabled = false
			err := m.namespace.SetBackupRepoSettings(context.Background(), settings)
			return backupsEnabledMsg{err: err}
		})

	case backupsProbeFinishedMsg:
		m.running = false
		if msg.err != nil {
			// The form stays populated: settings and password persisted,
			// Enabled withheld, restic's error verbatim.
			m.err = msg.err
			return m, nil
		}
		if m.enabled {
			// Custody was proven when backups were first enabled; a
			// reconfigure only revalidates.
			return m.startRunning("Enabling backups...", m.runEnable())
		}
		m.handoff = NewNamespaceCustodyHandoff(m.namespace)
		custody := NewCustody(NewRecoveryKit(msg.settings), m.handoff, CustodyDeliveryAsk)
		c, _ := custody.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		m.custody = &c
		return m, m.custody.Init()

	case backupsEnabledMsg:
		m.running = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		return m, func() tea.Msg { return BackupsFormCloseMsg{} }

	case ProgressTickMsg:
		if m.running {
			var cmd tea.Cmd
			m.progress, cmd = m.progress.Update(msg)
			return m, cmd
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.form, cmd = m.form.Update(msg)
	return m, cmd
}

func (m BackupsForm) View() string {
	if m.custody != nil {
		return m.custody.View()
	}

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Colors.Border).
		Padding(1, 4)

	title := lipgloss.NewStyle().Bold(true).Foreground(Colors.Primary).MarginBottom(1).Render("Backups")

	body := m.setupView()
	if m.running {
		status := lipgloss.NewStyle().Foreground(Colors.Muted).Render(m.status)
		// The detail row is always present, empty or not: a line appearing
		// mid-probe would shift the centered overlay under the user.
		detail := lipgloss.NewStyle().Foreground(Colors.Border).Render(m.detail)
		body = lipgloss.JoinVertical(lipgloss.Center, status, detail, "", m.progress.View())
	}

	helpLine := lipgloss.NewStyle().MarginTop(1).Align(lipgloss.Center).Render(m.help.View())

	return boxStyle.Render(lipgloss.JoinVertical(lipgloss.Center, title, body, helpLine))
}

// Private

func (m BackupsForm) updateCustody(msg tea.Msg) (Component, tea.Cmd) {
	// The closed message alone carries the verdict (it is custody component
	// state, not a separately racing message). Confirmed hands the overlay
	// back to the running path: EnableBackups completes - or fails onto the
	// form, where a retry is possible - before the overlay may close.
	if closed, ok := msg.(CustodyClosedMsg); ok {
		m.custody = nil
		// The flow takes its own phone handoff down; this is the backstop for
		// every way out of it, and Stop is idempotent.
		stop := m.stopHandoff()
		if !closed.Confirmed {
			return m, tea.Batch(stop, func() tea.Msg { return BackupsFormCloseMsg{} })
		}
		component, cmd := m.startRunning("Enabling backups...", m.runEnable())
		return component, tea.Batch(stop, cmd)
	}

	c, cmd := m.custody.Update(msg)
	m.custody = &c
	return m, cmd
}

func (m BackupsForm) stopHandoff() tea.Cmd {
	if m.handoff == nil {
		return nil
	}
	return stopHandoffCmd(m.handoff)
}

// startRunning switches to the progress view and runs cmd in the background.
func (m BackupsForm) startRunning(status string, cmd tea.Cmd) (Component, tea.Cmd) {
	m.running = true
	m.status = status
	m.detail = ""
	m.err = nil
	m.progress = NewProgress(m.width, Colors.Border)
	return m, tea.Batch(m.progress.Init(), cmd)
}

// runProbe persists and probes; the finished message carries the settings
// read back after ConfigureBackups, which mints the password when none
// exists yet - the recovery kit needs it. It reports through the progress
// seam, so the offsite leg's upload is visible while it happens.
func (m BackupsForm) runProbe(settings docker.BackupRepoSettings) tea.Cmd {
	ns := m.namespace
	return m.stream.Run(func(ctx context.Context, progress docker.DeployProgressCallback) tea.Msg {
		err := ns.ConfigureBackups(ctx, settings, progress)
		return backupsProbeFinishedMsg{settings: ns.BackupRepoSettings(), err: err}
	})
}

func (m BackupsForm) runEnable() tea.Cmd {
	ns := m.namespace
	return func() tea.Msg {
		return backupsEnabledMsg{err: ns.EnableBackups(context.Background())}
	}
}

func (m BackupsForm) setupView() string {
	var parts []string
	if m.err != nil {
		parts = append(parts, lipgloss.NewStyle().Foreground(Colors.Error).Width(backupsFormTextWidth).Render(docker.ErrorMessage(m.err)), "")
	}

	intro := "Backups are enabled. Enable re-validates the settings below\nwith a probe against each repository."
	if !m.enabled {
		intro = "One backup repository per install. Apps opt in per-app; the offsite\nURL is any restic repository, with its credentials named below."
	}
	parts = append(parts, lipgloss.NewStyle().Foreground(Colors.Muted).Render(intro), "", m.form.View())

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// Helpers

func newBackupsForm(ns *docker.Namespace, settings docker.BackupRepoSettings) BackupsForm {
	m := BackupsForm{
		namespace: ns,
		enabled:   settings.Enabled,
		help:      NewHelp(),
		progress:  NewProgress(0, Colors.Border),
	}
	m.help.SetBindings([]key.Binding{backupsFormCloseKey})

	localPath := settings.LocalPath
	if localPath == "" {
		localPath = docker.DefaultBackupLocalPath
	}
	localPathField := NewTextField(docker.DefaultBackupLocalPath)
	localPathField.SetValue(localPath)

	offsiteURLField := NewTextField("s3:s3.amazonaws.com/bucket/once (optional)")
	offsiteURLField.SetValue(settings.OffsiteURL)

	items := []FormItem{
		{Key: "local-path", Label: "Local repository path", Field: localPathField, Required: true},
		{Key: "offsite-url", Label: "Offsite repository URL", Field: offsiteURLField},
	}
	names := slices.Sorted(maps.Keys(settings.OffsiteEnv))
	for row, name := range names {
		items = append(items, newCredKeyItem(row, name), newCredValueItem(row, settings.OffsiteEnv[name]))
	}
	items = append(items, newCredKeyItem(len(names), ""), newCredValueItem(len(names), ""))

	m.form = NewForm("Enable", items...)

	// Typing into the last credential-name row grows a fresh empty pair, the
	// environment form's pattern.
	m.form.OnRebuild(func(f *Form) {
		lastRow := (f.ItemCount() - 2) / 2
		if lastRow >= 1 && f.TextField(credKeyFieldKey(lastRow-1)).Value() != "" {
			f.AppendItems(newCredKeyItem(lastRow, ""), newCredValueItem(lastRow, ""))
		}
	})

	m.form.OnSubmit(func(f *Form) tea.Cmd {
		s := settings
		s.LocalPath = strings.TrimSpace(f.TextField("local-path").Value())
		s.OffsiteURL = strings.TrimSpace(f.TextField("offsite-url").Value())
		s.OffsiteEnv = nil
		for row := range (f.ItemCount() - 2) / 2 {
			name := strings.TrimSpace(f.TextField(credKeyFieldKey(row)).Value())
			if name == "" {
				continue
			}
			if s.OffsiteEnv == nil {
				s.OffsiteEnv = make(map[string]string)
			}
			s.OffsiteEnv[name] = f.TextField(credValueFieldKey(row)).Value()
		}

		if !filepath.IsAbs(s.LocalPath) {
			f.SetError("local-path", "The local repository path must be absolute")
			return nil
		}
		if err := docker.ValidateOffsiteURL(s.OffsiteURL); err != nil {
			f.SetError("offsite-url", docker.ErrorMessage(err))
			return nil
		}

		// The password is deliberately not touched here: ConfigureBackups
		// owns it - reusing the persisted one, minting only when none has
		// ever existed - so a resubmit after a failed probe can never
		// overwrite the password a repo was already initialized with.
		return func() tea.Msg { return backupsEnableSubmitMsg{settings: s} }
	})
	if settings.Enabled {
		m.form.SetActionButton("Disable", func() tea.Msg { return backupsDisableSubmitMsg{} })
	}
	m.form.OnCancel(func(f *Form) tea.Cmd {
		return func() tea.Msg { return BackupsFormCloseMsg{} }
	})

	return m
}

func newCredKeyItem(row int, value string) FormItem {
	f := NewTextField("AWS_ACCESS_KEY_ID")
	f.SetValue(value)
	return FormItem{Key: credKeyFieldKey(row), Label: fmt.Sprintf("Credential %d name", row+1), Field: f}
}

func newCredValueItem(row int, value string) FormItem {
	f := NewTextField("value")
	f.SetValue(value)
	f.SetEchoPassword()
	f.SetCharLimit(1024)
	return FormItem{Key: credValueFieldKey(row), Label: fmt.Sprintf("Credential %d value", row+1), Field: f}
}

func credKeyFieldKey(row int) string {
	return fmt.Sprintf("cred-key-%d", row)
}

func credValueFieldKey(row int) string {
	return fmt.Sprintf("cred-value-%d", row)
}
