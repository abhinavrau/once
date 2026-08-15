package ui

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/once/internal/docker"
)

// RecoveryKit is everything bare-metal recovery needs: the password alone is
// not enough on a fresh host, so the kit also names the repos and the backend
// credential names (never the values - the user holds those at their
// provider).
type RecoveryKit struct {
	Password        string
	LocalPath       string
	OffsiteURL      string
	CredentialNames []string
	CreatedAt       time.Time
	Hostname        string
}

func NewRecoveryKit(cfg docker.BackupRepoSettings) RecoveryKit {
	hostname, _ := os.Hostname()
	return RecoveryKit{
		Password:        cfg.Password,
		LocalPath:       cfg.LocalPath,
		OffsiteURL:      cfg.OffsiteURL,
		CredentialNames: slices.Sorted(maps.Keys(cfg.OffsiteEnv)),
		CreatedAt:       time.Now(),
		Hostname:        hostname,
	}
}

// Text is the plain-text kit: the ctrl+k clipboard payload, and the block the
// no-TTY banner prints.
func (k RecoveryKit) Text() string {
	lines := []string{
		"ONCE RECOVERY KIT - keep this whole block",
		"Backup password:  " + docker.FormatRepoPassword(k.Password),
	}
	if k.OffsiteURL != "" {
		lines = append(lines, "Offsite repo:     "+k.OffsiteURL)
	}
	if len(k.CredentialNames) > 0 {
		lines = append(lines, "Credential names: "+strings.Join(k.CredentialNames, ", ")+" (values are at your provider)")
	}
	lines = append(lines,
		"Local repo:       "+k.LocalPath,
		"Created:          "+k.CreatedAt.Format("2006-01-02")+" on "+k.Hostname,
	)
	return strings.Join(lines, "\n")
}

// The custody flow: the one moment the user takes custody of the Repo
// Password. Reveal (the recovery kit under a copy / store / prove checklist),
// then an unmasked type-back gate, with a loud skip hatch. One shared
// component for both frontends: the TUI hosts it in the Backups overlay, the
// CLI runs it as a sequential bubbletea program the way cli_progress.go
// already runs progress.
//
// custodyDeliveries lists the ways the password can be handed over. With one
// option the choice prompt collapses to nothing to choose; the QR delivery
// (issue 14) appends a second option and the prompt reappears.
var custodyDeliveries = []string{"Show it on this screen"}

type custodyStep int

const (
	custodyStepChoice custodyStep = iota
	custodyStepReveal
	custodyStepGate
	custodyStepSkipConfirm
	custodyStepDone
	custodyStepSkipped
)

var custodyKeys = struct {
	CopyPassword key.Binding
	CopyKit      key.Binding
	Continue     key.Binding
}{
	CopyPassword: WithHelp(NewKeyBinding("ctrl+p"), "ctrl+p", "copy password"),
	CopyKit:      WithHelp(NewKeyBinding("ctrl+k"), "ctrl+k", "copy kit"),
	Continue:     WithHelp(NewKeyBinding("enter"), "enter", "continue"),
}

// CustodyResultMsg reports the flow's verdict the moment it is decided: the
// gate passed, or the skip was confirmed. The parent flips Enabled here.
type CustodyResultMsg struct{ Confirmed bool }

// CustodyClosedMsg reports that the user dismissed the final screen.
type CustodyClosedMsg struct{ Confirmed bool }

type (
	custodyGatePassedMsg struct{}
	custodyGateCancelMsg struct{}
)

type Custody struct {
	kit           RecoveryKit
	step          custodyStep
	choice        int
	gate          Form
	copied        string // "", "password", or "kit"
	confirmed     bool
	width, height int
}

func NewCustody(kit RecoveryKit) Custody {
	m := Custody{kit: kit, step: custodyStepChoice}
	if len(custodyDeliveries) == 1 {
		m.step = custodyStepReveal
	}
	m.gate = newCustodyGateForm(kit.Password)
	return m
}

func (m Custody) Init() tea.Cmd {
	return nil
}

// ShowingSecret reports whether the current screen displays the password.
// Mouse tracking must be off there so native terminal selection keeps
// working; the gate keeps tracking for click-to-focus.
func (m Custody) ShowingSecret() bool {
	return m.step == custodyStepReveal
}

func (m Custody) Update(msg tea.Msg) (Custody, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.gate, _ = m.gate.Update(msg)
		return m, nil

	case custodyGatePassedMsg:
		m.step = custodyStepDone
		m.confirmed = true
		return m, func() tea.Msg { return CustodyResultMsg{Confirmed: true} }

	case custodyGateCancelMsg:
		m.step = custodyStepSkipConfirm
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	if m.step == custodyStepGate {
		var cmd tea.Cmd
		m.gate, cmd = m.gate.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Custody) View() string {
	switch m.step {
	case custodyStepChoice:
		return m.choiceView()
	case custodyStepReveal:
		return m.revealView()
	case custodyStepGate:
		return m.gateView()
	case custodyStepSkipConfirm:
		return m.skipConfirmView()
	case custodyStepDone:
		return m.doneView()
	case custodyStepSkipped:
		return m.skippedView()
	}
	return ""
}

// Private

func (m Custody) handleKey(msg tea.KeyPressMsg) (Custody, tea.Cmd) {
	switch m.step {
	case custodyStepChoice:
		switch msg.String() {
		case "up", "down", "j", "k", "tab":
			m.choice = (m.choice + 1) % len(custodyDeliveries)
		case "enter":
			m.step = custodyStepReveal
		}
		return m, nil

	case custodyStepReveal:
		switch {
		case key.Matches(msg, custodyKeys.CopyPassword):
			m.copied = "password"
			return m, tea.SetClipboard(m.kit.Password)
		case key.Matches(msg, custodyKeys.CopyKit):
			m.copied = "kit"
			return m, tea.SetClipboard(m.kit.Text())
		case msg.String() == "enter":
			m.step = custodyStepGate
			return m, m.gate.Init()
		}
		return m, nil

	case custodyStepGate:
		if msg.String() == "esc" {
			m.step = custodyStepSkipConfirm
			return m, nil
		}
		var cmd tea.Cmd
		m.gate, cmd = m.gate.Update(msg)
		return m, cmd

	case custodyStepSkipConfirm:
		switch msg.String() {
		case "enter", "esc":
			// Rebuild so the field is focused and empty again.
			m.gate = newCustodyGateForm(m.kit.Password)
			m.gate, _ = m.gate.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
			m.step = custodyStepGate
			return m, m.gate.Init()
		case "s":
			m.step = custodyStepSkipped
			return m, func() tea.Msg { return CustodyResultMsg{Confirmed: false} }
		}
		return m, nil

	case custodyStepDone, custodyStepSkipped:
		if msg.String() == "enter" {
			confirmed := m.confirmed
			return m, func() tea.Msg { return CustodyClosedMsg{Confirmed: confirmed} }
		}
		return m, nil
	}
	return m, nil
}

func (m Custody) choiceView() string {
	intro := custodyMuted("Once generated the password that encrypts every backup.\n" +
		"It is about to be shown exactly once - it is not stored anywhere\n" +
		"you can read it back later. How do you want to save it?")

	var lines []string
	for i, opt := range custodyDeliveries {
		cursor, style := "  ", lipgloss.NewStyle()
		if i == m.choice {
			cursor = "> "
			style = style.Foreground(Colors.Focused).Bold(true)
		}
		lines = append(lines, style.Render(cursor+opt))
	}

	body := lipgloss.JoinVertical(lipgloss.Left,
		intro, "", strings.Join(lines, "\n"), custodyHelpLine("↑/↓ choose", "enter continue"))
	return custodyBox().Render(lipgloss.JoinVertical(lipgloss.Center, custodyTitle("Save your backup password"), body))
}

// revealView is prototype variant D: the numbered copy / store / prove
// checklist with the recovery kit block under step 1, trimmed to the 80x24
// floor (the kit block loses its vertical padding and subtitle).
func (m Custody) revealView() string {
	num := lipgloss.NewStyle().Bold(true).Foreground(Colors.Primary)

	steps := lipgloss.JoinVertical(lipgloss.Left,
		num.Render("1. Copy it")+"    "+custodyMuted("("+m.copyHint("kit", "ctrl+k whole kit")+" · "+m.copyHint("password", "ctrl+p password only")+")"),
		"",
		m.kitBlock(),
		"",
		num.Render("2. Store it")+"   "+custodyMuted("off this machine: password manager notes, printed page,\n   anywhere that survives this server dying."),
		"",
		num.Render("3. Prove it")+"   "+custodyMuted("the next screen asks you to type the password back."),
	)

	return custodyBox().Render(lipgloss.JoinVertical(lipgloss.Center,
		custodyTitle("Save your recovery kit"), steps,
		custodyHelpLine("select with the mouse to copy", "enter continue")))
}

func (m Custody) kitBlock() string {
	label := lipgloss.NewStyle().Bold(true)
	row := func(k, v string) string { return label.Render(fmt.Sprintf("%-13s", k)) + v }

	rows := []string{
		lipgloss.NewStyle().Bold(true).Foreground(Colors.Primary).Render("ONCE RECOVERY KIT"),
		row("Password", docker.FormatRepoPassword(m.kit.Password)),
	}
	if m.kit.OffsiteURL != "" {
		rows = append(rows, row("Offsite repo", m.kit.OffsiteURL))
	}
	if len(m.kit.CredentialNames) > 0 {
		rows = append(rows, row("Credentials", strings.Join(m.kit.CredentialNames, ", ")+" (names only)"))
	}
	rows = append(rows,
		row("Local repo", m.kit.LocalPath),
		row("Created", m.kit.CreatedAt.Format("2006-01-02")+" on "+m.kit.Hostname),
	)

	return lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).BorderForeground(Colors.Primary).Padding(0, 2).
		Render(lipgloss.JoinVertical(lipgloss.Left, rows...))
}

func (m Custody) gateView() string {
	body := lipgloss.JoinVertical(lipgloss.Left,
		custodyMuted("Type the password back from what you just saved to prove it is\n"+
			"saved. Pasting is fine - the point is that it now lives somewhere\n"+
			"off this server. Dashes and case don't matter."),
		"",
		m.gate.View())
	return custodyBox().Render(lipgloss.JoinVertical(lipgloss.Center, custodyTitle("Prove it's saved"), body))
}

func (m Custody) skipConfirmView() string {
	warn := lipgloss.NewStyle().Bold(true).Foreground(Colors.Warning).Render("Skip without confirming?")
	body := lipgloss.JoinVertical(lipgloss.Left,
		warn, "",
		custodyMuted("The password stays saved on this server, but backups stay OFF\n"+
			"until you confirm it with `once backup enable`. Nothing is lost\n"+
			"yet - but nothing is protected either."),
		custodyHelpLine("enter go back and confirm", "s skip anyway"))
	return custodyBox().Render(body)
}

func (m Custody) doneView() string {
	check := lipgloss.NewStyle().Foreground(Colors.Success).Bold(true).Render("✓ Password confirmed - backups are on")
	body := lipgloss.JoinVertical(lipgloss.Center,
		check, "",
		custodyMuted("The first backup runs with the next daemon tick."),
		custodyHelpLine("enter close"))
	return custodyBox().Render(body)
}

func (m Custody) skippedView() string {
	body := lipgloss.JoinVertical(lipgloss.Center,
		lipgloss.NewStyle().Bold(true).Foreground(Colors.Warning).Render("Backups are NOT enabled"),
		"",
		custodyMuted("Run `once backup enable` when you are ready to confirm\nthe password."),
		custodyHelpLine("enter close"))
	return custodyBox().Render(body)
}

// copyHint renders label, or green feedback when this kind was just copied.
func (m Custody) copyHint(kind, label string) string {
	if m.copied == kind {
		return lipgloss.NewStyle().Foreground(Colors.Success).Render(kind + " copied ✓")
	}
	return label
}

// Helpers

func newCustodyGateForm(password string) Form {
	field := NewTextField("type or paste the password")
	form := NewForm("Confirm",
		FormItem{Key: "password", Label: "Backup password", Field: field, Required: true},
	)
	form.OnSubmit(func(f *Form) tea.Cmd {
		if docker.NormalizeRepoPassword(f.TextField("password").Value()) != password {
			f.SetError("password", "That doesn't match. Check what you saved - fixing it now is free;\nfinding out during a disaster is not.")
			return nil
		}
		return func() tea.Msg { return custodyGatePassedMsg{} }
	})
	form.OnCancel(func(f *Form) tea.Cmd {
		return func() tea.Msg { return custodyGateCancelMsg{} }
	})
	return form
}

func custodyBox() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Colors.Border).
		Padding(1, 2)
}

func custodyTitle(s string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(Colors.Primary).MarginBottom(1).Render(s)
}

func custodyMuted(s string) string {
	return lipgloss.NewStyle().Foreground(Colors.Muted).Render(s)
}

func custodyHelpLine(bindings ...string) string {
	return lipgloss.NewStyle().MarginTop(1).Foreground(Colors.Muted).Render(strings.Join(bindings, " · "))
}
