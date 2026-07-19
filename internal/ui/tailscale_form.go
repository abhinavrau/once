package ui

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/once/internal/docker"
)

const (
	tailscaleClientIDField = iota
	tailscaleClientSecretField
	tailscaleTagField
)

const tailscaleFormTextWidth = 72

var (
	tailscaleFormCloseKey = WithHelp(NewKeyBinding("esc"), "esc", "close")
	tailscaleCopyKey      = WithHelp(NewKeyBinding("c", "C"), "c", "copy snippet")
)

type TailscaleFormCloseMsg struct{}

type tailscaleEnableSubmitMsg struct {
	id, secret, tag string
}

type tailscaleDisableSubmitMsg struct{}

// tailscaleCloseRequestMsg funnels every close intent (esc, Cancel buttons)
// through one Update branch, so the one-shot policy client is always cleaned
// up before the overlay closes.
type tailscaleCloseRequestMsg struct{}

// tailscaleConsentMsg carries the planned policy update into the consent view,
// along with the bootstrap handle and one-shot policy client needed to resolve
// the decision.
type tailscaleConsentMsg struct {
	bootstrap      *docker.TailscaleBootstrap
	policyClientID string
	update         *docker.PolicyUpdate
}

// tailscaleSnippetMsg switches to the copyable-snippet view: after a decline,
// or after a policy step failure (note explains which; empty for a decline).
type tailscaleSnippetMsg struct {
	bootstrap *docker.TailscaleBootstrap
	note      string
}

type tailscaleApplySubmitMsg struct{}
type tailscaleDeclineMsg struct{}
type tailscaleContinueMsg struct{}

// tailscalePolicyRevokeFailedMsg surfaces a failed revoke of the one-shot
// policy client, so a live policy_file credential is never leaked silently.
type tailscalePolicyRevokeFailedMsg struct{ err error }

type tailscaleFormFinishedMsg struct {
	err      error
	warnings []string
}

type tailscaleFormState int

const (
	tailscaleFormStateSetup tailscaleFormState = iota
	tailscaleFormStateConsent
	tailscaleFormStateSnippet
)

// TailscaleForm is the dashboard-global overlay for enabling/disabling
// Tailscale. Enabling is the full bootstrap flow the CLI runs: exchange the
// Bootstrap Credential, plan the tailnet policy changes, show the diff for
// consent (Apply writes it, Decline shows the copyable snippet), then mint the
// Runtime Credential and boot the Docktail topology.
type TailscaleForm struct {
	namespace     *docker.Namespace
	state         tailscaleFormState
	enabled       bool
	form          Form
	consentForm   Form
	snippetForm   Form
	help          Help
	width, height int
	running       bool
	progress      Progress
	err           error
	warnings      []string

	// Enable-flow state carried between the async steps.
	tag            string
	bootstrap      *docker.TailscaleBootstrap
	policyClientID string
	update         *docker.PolicyUpdate
	snippetNote    string
}

func NewTailscaleForm(ns *docker.Namespace) TailscaleForm {
	enabled, _ := ns.Tailscale().Enabled(context.Background())

	h := NewHelp()
	h.SetBindings([]key.Binding{tailscaleFormCloseKey})

	m := TailscaleForm{
		namespace: ns,
		enabled:   enabled,
		help:      h,
		progress:  NewProgress(0, Colors.Border),
	}

	if enabled {
		m.form = NewForm("Disable")
		m.form.OnSubmit(func(f *Form) tea.Cmd {
			return func() tea.Msg { return tailscaleDisableSubmitMsg{} }
		})
	} else {
		clientIDField := NewTextField("K123456CNTRL")

		clientSecretField := NewTextField("tskey-client-...")
		clientSecretField.SetEchoPassword()

		tagField := NewTextField("tag:once")
		tagField.SetValue("tag:once")

		m.form = NewForm("Enable",
			FormItem{Label: "Bootstrap Client ID", Field: clientIDField, Required: true},
			FormItem{Label: "Bootstrap Client Secret", Field: clientSecretField, Required: true},
			FormItem{Label: "Tag", Field: tagField, Required: true},
		)
		m.form.OnSubmit(func(f *Form) tea.Cmd {
			return func() tea.Msg {
				return tailscaleEnableSubmitMsg{
					id:     f.TextField(tailscaleClientIDField).Value(),
					secret: f.TextField(tailscaleClientSecretField).Value(),
					tag:    f.TextField(tailscaleTagField).Value(),
				}
			}
		})
	}
	m.form.OnCancel(requestTailscaleFormClose)

	return m
}

func (m TailscaleForm) Init() tea.Cmd {
	return m.form.Init()
}

func (m TailscaleForm) Update(msg tea.Msg) (Component, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.SetWidth(m.width)
		m.progress = m.progress.SetWidth(m.width)
		m.form, _ = m.form.Update(msg)
		m.consentForm, _ = m.consentForm.Update(msg)
		m.snippetForm, _ = m.snippetForm.Update(msg)
		return m, nil

	case tea.KeyPressMsg:
		if m.running {
			return m, nil
		}
		if key.Matches(msg, tailscaleFormCloseKey) {
			cmd := m.close()
			return m, cmd
		}
		if m.state == tailscaleFormStateSnippet && key.Matches(msg, tailscaleCopyKey) {
			return m, tea.SetClipboard(docker.PolicySnippet(m.tag))
		}
		m.err = nil
		m.warnings = nil

	case MouseEvent:
		if m.running {
			return m, nil
		}

	case tailscaleCloseRequestMsg:
		cmd := m.close()
		return m, cmd

	case tailscaleEnableSubmitMsg:
		m.tag = msg.tag
		return m.startRunning(m.beginEnable(msg))

	case tailscaleDisableSubmitMsg:
		return m.startRunning(func() tea.Msg {
			return tailscaleFormFinishedMsg{err: m.namespace.DisableTailscale(context.Background())}
		})

	case tailscaleConsentMsg:
		m.running = false
		m.bootstrap = msg.bootstrap
		m.policyClientID = msg.policyClientID
		m.update = msg.update
		m.state = tailscaleFormStateConsent
		m.consentForm = m.newConsentForm()
		return m, m.consentForm.Init()

	case tailscaleApplySubmitMsg:
		cmd := m.applyPolicy()
		m.policyClientID = ""
		m.update = nil
		return m.startRunning(cmd)

	case tailscaleDeclineMsg:
		cleanup := m.deletePolicyClient()
		m.showSnippet("")
		return m, tea.Batch(cleanup, m.snippetForm.Init())

	case tailscaleSnippetMsg:
		m.running = false
		m.bootstrap = msg.bootstrap
		m.showSnippet(msg.note)
		return m, m.snippetForm.Init()

	case tailscaleContinueMsg:
		return m.startRunning(m.finishEnable())

	case tailscalePolicyRevokeFailedMsg:
		note := docker.PolicyRevokeFailureNotice + ": " + docker.ErrorMessage(msg.err)
		if m.state == tailscaleFormStateSnippet {
			m.snippetNote = strings.TrimSpace(note + "\n" + m.snippetNote)
		} else {
			m.warnings = append(m.warnings, note)
		}
		return m, nil

	case tailscaleFormFinishedMsg:
		m.running = false
		if msg.err != nil {
			m.err = msg.err
			m.backToSetup()
			return m, nil
		}
		// Shadowing warnings keep the form open so the user actually sees them.
		if len(msg.warnings) > 0 {
			m.warnings = msg.warnings
			m.backToSetup()
			return m, nil
		}
		return m, func() tea.Msg { return TailscaleFormCloseMsg{} }

	case ProgressTickMsg:
		if m.running {
			var cmd tea.Cmd
			m.progress, cmd = m.progress.Update(msg)
			return m, cmd
		}
		return m, nil
	}

	var cmd tea.Cmd
	switch m.state {
	case tailscaleFormStateConsent:
		m.consentForm, cmd = m.consentForm.Update(msg)
	case tailscaleFormStateSnippet:
		m.snippetForm, cmd = m.snippetForm.Update(msg)
	default:
		m.form, cmd = m.form.Update(msg)
	}
	return m, cmd
}

func (m TailscaleForm) View() string {
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Colors.Border).
		Padding(1, 4)

	title := lipgloss.NewStyle().Bold(true).Foreground(Colors.Primary).MarginBottom(1).Render("Tailscale")

	var body string
	switch {
	case m.running:
		body = m.progress.View()
	case m.state == tailscaleFormStateConsent:
		body = m.consentView()
	case m.state == tailscaleFormStateSnippet:
		body = m.snippetView()
	default:
		body = m.setupView()
	}

	helpLine := lipgloss.NewStyle().MarginTop(1).Align(lipgloss.Center).Render(m.help.View())

	return boxStyle.Render(lipgloss.JoinVertical(lipgloss.Center, title, body, helpLine))
}

// Private

// startRunning switches to the progress view and runs cmd in the background.
func (m TailscaleForm) startRunning(cmd tea.Cmd) (Component, tea.Cmd) {
	m.running = true
	m.err = nil
	m.warnings = nil
	m.progress = NewProgress(m.width, Colors.Border)
	return m, tea.Batch(m.progress.Init(), cmd)
}

// beginEnable runs the first async leg of the enable flow: exchange the
// Bootstrap Credential, mint the one-shot policy client, and plan the policy
// update for the consent view. Any policy step failure falls through to the
// snippet view and the enable can still proceed - the credential probe at
// enable time is the enforcement, mirroring the CLI. Under the headscale
// control seam there is no SaaS OAuth, so enable runs directly.
func (m TailscaleForm) beginEnable(msg tailscaleEnableSubmitMsg) tea.Cmd {
	ns := m.namespace
	return func() tea.Msg {
		ctx := context.Background()
		if docker.TailscaleControlSeamActive() {
			warnings, err := ns.EnableTailscale(ctx, docker.TailscaleSettings{Tag: msg.tag})
			return tailscaleFormFinishedMsg{err: err, warnings: warnings}
		}

		bootstrap, err := docker.NewTailscaleBootstrap(ctx, msg.id, msg.secret)
		if err != nil {
			return tailscaleFormFinishedMsg{err: err}
		}

		policyID, policySecret, err := bootstrap.MintPolicyClient(ctx)
		if err != nil {
			return tailscaleSnippetMsg{bootstrap: bootstrap, note: "Could not mint the policy OAuth client: " + docker.ErrorMessage(err)}
		}
		update, err := docker.PlanPolicyUpdate(ctx, policyID, policySecret, msg.tag)
		if err != nil {
			note := "Could not plan the policy update: " + docker.ErrorMessage(err) + revokeNote(ctx, bootstrap, policyID)
			return tailscaleSnippetMsg{bootstrap: bootstrap, note: note}
		}
		if !update.Changed() {
			note := revokeNote(ctx, bootstrap, policyID)
			return withRevokeWarning(enableWithBootstrap(ctx, ns, bootstrap, msg.tag), note)
		}
		return tailscaleConsentMsg{bootstrap: bootstrap, policyClientID: policyID, update: update}
	}
}

// applyPolicy writes the consented policy change, deletes the one-shot policy
// client either way, and continues into the enable. A failed write falls back
// to the snippet view rather than aborting.
func (m TailscaleForm) applyPolicy() tea.Cmd {
	ns, bootstrap, update, policyClientID, tag := m.namespace, m.bootstrap, m.update, m.policyClientID, m.tag
	return func() tea.Msg {
		ctx := context.Background()
		applyErr := update.Apply(ctx)
		note := revokeNote(ctx, bootstrap, policyClientID)
		if applyErr != nil {
			return tailscaleSnippetMsg{bootstrap: bootstrap, note: "The policy write failed: " + docker.ErrorMessage(applyErr) + note}
		}
		return withRevokeWarning(enableWithBootstrap(ctx, ns, bootstrap, tag), note)
	}
}

// finishEnable continues the enable after the snippet view: mint the Runtime
// Credential and boot the topology.
func (m TailscaleForm) finishEnable() tea.Cmd {
	ns, bootstrap, tag := m.namespace, m.bootstrap, m.tag
	return func() tea.Msg {
		return enableWithBootstrap(context.Background(), ns, bootstrap, tag)
	}
}

func (m *TailscaleForm) backToSetup() {
	m.state = tailscaleFormStateSetup
	m.help.SetBindings([]key.Binding{tailscaleFormCloseKey})
}

// deletePolicyClient revokes the outstanding one-shot policy client, best
// effort, and forgets it.
func (m *TailscaleForm) deletePolicyClient() tea.Cmd {
	if m.policyClientID == "" {
		return nil
	}
	bootstrap, clientID := m.bootstrap, m.policyClientID
	m.policyClientID = ""
	m.update = nil
	return func() tea.Msg {
		if err := bootstrap.DeleteClient(context.Background(), clientID); err != nil {
			return tailscalePolicyRevokeFailedMsg{err: err}
		}
		return nil
	}
}

// close cleans up any outstanding one-shot policy client and closes the overlay.
func (m *TailscaleForm) close() tea.Cmd {
	cleanup := m.deletePolicyClient()
	return tea.Batch(cleanup, func() tea.Msg { return TailscaleFormCloseMsg{} })
}

func (m *TailscaleForm) showSnippet(note string) {
	m.running = false
	m.state = tailscaleFormStateSnippet
	m.snippetNote = note
	m.snippetForm = NewForm("Continue")
	m.snippetForm.OnSubmit(func(f *Form) tea.Cmd {
		return func() tea.Msg { return tailscaleContinueMsg{} }
	})
	m.snippetForm.OnCancel(requestTailscaleFormClose)
	m.help.SetBindings([]key.Binding{tailscaleCopyKey, tailscaleFormCloseKey})
	if m.width > 0 {
		m.snippetForm, _ = m.snippetForm.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	}
}

func (m TailscaleForm) newConsentForm() Form {
	f := NewForm("Apply")
	f.OnSubmit(func(f *Form) tea.Cmd {
		return func() tea.Msg { return tailscaleApplySubmitMsg{} }
	})
	f.SetActionButton("Decline", func() tea.Msg { return tailscaleDeclineMsg{} })
	f.OnCancel(requestTailscaleFormClose)
	if m.width > 0 {
		f, _ = f.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	}
	return f
}

func (m TailscaleForm) setupView() string {
	var parts []string
	if m.err != nil {
		parts = append(parts, lipgloss.NewStyle().Foreground(Colors.Error).Width(tailscaleFormTextWidth).Render(docker.ErrorMessage(m.err)), "")
	} else if len(m.warnings) > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(Colors.Warning).Width(tailscaleFormTextWidth).Render(strings.Join(m.warnings, "\n")), "")
	}

	intro := "Tailscale is enabled."
	if !m.enabled {
		intro = "Create one OAuth client with the oauth_keys scope in the Tailscale\nadmin console (Settings -> Trust credentials) and paste it here.\nOnce mints and manages everything else."
	}
	parts = append(parts, lipgloss.NewStyle().Foreground(Colors.Muted).Render(intro), "", m.form.View())

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m TailscaleForm) consentView() string {
	intro := lipgloss.NewStyle().Width(tailscaleFormTextWidth).Render(docker.PolicyConsentIntro)
	warning := lipgloss.NewStyle().Foreground(Colors.Warning).Width(tailscaleFormTextWidth).Render(docker.PolicyGitOpsWarning)

	return lipgloss.JoinVertical(lipgloss.Left, intro, "", renderPolicyDiff(m.update.Diff()), "", warning, "", m.consentForm.View())
}

func (m TailscaleForm) snippetView() string {
	var parts []string
	if m.snippetNote != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(Colors.Warning).Width(tailscaleFormTextWidth).Render(m.snippetNote), "")
	}
	intro := lipgloss.NewStyle().Width(tailscaleFormTextWidth).Render(
		"Merge this snippet into your tailnet policy file (https://login.tailscale.com/admin/acls), then continue; enable will fail with a probe error until the tag exists.")
	parts = append(parts, intro, "", expandTabs(docker.PolicySnippet(m.tag)), "", m.snippetForm.View())

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// Helpers

func requestTailscaleFormClose(f *Form) tea.Cmd {
	return func() tea.Msg { return tailscaleCloseRequestMsg{} }
}

// enableWithBootstrap is the tail of the enable flow shared by every consent
// outcome: mint the Runtime Credential (the only one persisted) and run the
// same Namespace lifecycle the CLI uses.
func enableWithBootstrap(ctx context.Context, ns *docker.Namespace, bootstrap *docker.TailscaleBootstrap, tag string) tea.Msg {
	id, secret, err := bootstrap.MintRuntimeClient(ctx, tag)
	if err != nil {
		return tailscaleFormFinishedMsg{err: err}
	}
	warnings, err := ns.EnableTailscale(ctx, docker.TailscaleSettings{ClientID: id, ClientSecret: secret, Tag: tag})
	return tailscaleFormFinishedMsg{err: err, warnings: warnings}
}

// revokeNote deletes the one-shot policy client and returns a note to append
// to whatever surface comes next when the revoke fails, mirroring the CLI's
// revoke-manually message.
func revokeNote(ctx context.Context, bootstrap *docker.TailscaleBootstrap, clientID string) string {
	if err := bootstrap.DeleteClient(ctx, clientID); err != nil {
		return "\n" + docker.PolicyRevokeFailureNotice + ": " + docker.ErrorMessage(err)
	}
	return ""
}

// withRevokeWarning attaches a revoke-failure note to a successful enable
// result as a warning, which keeps the form open long enough to be seen.
func withRevokeWarning(msg tea.Msg, note string) tea.Msg {
	fin, ok := msg.(tailscaleFormFinishedMsg)
	if note == "" || !ok || fin.err != nil {
		return msg
	}
	fin.warnings = append(fin.warnings, strings.TrimSpace(note))
	return fin
}

// renderPolicyDiff colors a unified diff's added/removed lines.
func renderPolicyDiff(diff string) string {
	added := lipgloss.NewStyle().Foreground(Colors.Success)
	removed := lipgloss.NewStyle().Foreground(Colors.Error)

	lines := strings.Split(strings.TrimRight(expandTabs(diff), "\n"), "\n")
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "+"):
			lines[i] = added.Render(line)
		case strings.HasPrefix(line, "-"):
			lines[i] = removed.Render(line)
		}
	}
	return strings.Join(lines, "\n")
}

// expandTabs replaces the policy HuJSON's tab indentation, which terminal
// cells and width measurement handle unpredictably inside a bordered box.
func expandTabs(s string) string {
	return strings.ReplaceAll(s, "\t", "  ")
}
