package ui

import (
	"context"
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
	"github.com/basecamp/once/internal/handoff"
	"github.com/basecamp/once/internal/mouse"
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
	lines := []string{"ONCE RECOVERY KIT - keep this whole block"}
	for _, row := range k.rows() {
		lines = append(lines, fmt.Sprintf("%-13s %s", row.label+":", row.value))
	}
	return strings.Join(lines, "\n")
}

type kitRow struct {
	label, value string
}

// rows is the kit's single row source: the reveal block and the plain-text
// kit render the same facts, with the offsite lines omitted when there is no
// offsite repo.
func (k RecoveryKit) rows() []kitRow {
	rows := []kitRow{{"Password", docker.FormatRepoPassword(k.Password)}}
	if k.OffsiteURL != "" {
		rows = append(rows, kitRow{"Offsite repo", k.OffsiteURL})
	}
	if len(k.CredentialNames) > 0 {
		rows = append(rows, kitRow{"Credentials", strings.Join(k.CredentialNames, ", ") + " (names only)"})
	}
	return append(rows,
		kitRow{"Local repo", k.LocalPath},
		kitRow{"Created", k.CreatedAt.Format("2006-01-02") + " on " + k.Hostname},
	)
}

// The custody flow: the one moment the user takes custody of the Repo
// Password. Reveal (the recovery kit under a copy / store / prove checklist),
// then an unmasked type-back gate, with a loud skip hatch. One shared
// component for both frontends: the TUI hosts it in the Backups overlay, the
// CLI runs it as a sequential bubbletea program the way cli_progress.go
// already runs progress.
//
// The user is asked how to take custody first: on this screen, or sent to
// their phone as a QR code of a one-time URL.

// CustodyDelivery is how the user takes custody of the password. It is also
// what --password-delivery names, so the values are the flag's own words.
type CustodyDelivery string

const (
	// CustodyDeliveryAsk leaves the choice to the user.
	CustodyDeliveryAsk    CustodyDelivery = ""
	CustodyDeliveryScreen CustodyDelivery = "screen"
	CustodyDeliveryQR     CustodyDelivery = "qr"
)

// CustodyHandoff publishes the Recovery Kit at a one-time URL a phone can
// open, and takes it down again. The custody screen sees only the URL: which
// origin it is, and what had to be built to route there, stay behind this.
type CustodyHandoff interface {
	Start(ctx context.Context, kit RecoveryKit) (string, error)
	Stop(ctx context.Context) error
}

type custodyOption struct {
	delivery CustodyDelivery
	label    string
}

// custodyDeliveries lists the ways the password can be handed over, in the
// order they are offered. Showing it on screen always works. The phone handoff
// is offered only when there is somewhere to serve it and a terminal that can
// draw a code worth scanning - a QR this terminal will mangle sends the user
// to a phone that will never read it, which is worse than not offering it.
//
// With one option the choice prompt collapses to nothing to choose.
func custodyDeliveries(handoff CustodyHandoff) []custodyOption {
	options := []custodyOption{{CustodyDeliveryScreen, "Show it on this screen"}}
	if handoff != nil && QRUnavailableReason() == "" {
		options = append(options, custodyOption{CustodyDeliveryQR, "Send it to my phone (QR code)"})
	}
	return options
}

func custodyDeliveryTarget(delivery CustodyDelivery) string {
	return "delivery:" + string(delivery)
}

type custodyStep int

const (
	custodyStepChoice custodyStep = iota
	custodyStepReveal
	custodyStepQRIntro
	custodyStepQR
	custodyStepGate
	custodyStepSkipConfirm
	custodyStepDone
	custodyStepSkipped
)

// custodyHandoffStartedMsg carries the URL the QR will encode, or the reason
// there is none.
type custodyHandoffStartedMsg struct {
	url string
	err error
}

var custodyKeys = struct {
	CopyPassword key.Binding
	CopyKit      key.Binding
	Continue     key.Binding
}{
	CopyPassword: WithHelp(NewKeyBinding("ctrl+p"), "ctrl+p", "copy password"),
	CopyKit:      WithHelp(NewKeyBinding("ctrl+k"), "ctrl+k", "copy kit"),
	Continue:     WithHelp(NewKeyBinding("enter"), "enter", "continue"),
}

// CustodyClosedMsg reports that the user dismissed the final screen, and is
// the flow's single outcome message: Confirmed is component state set
// synchronously when the gate passed, so no parent ever has to order two
// concurrently-delivered messages to learn the verdict. The parent flips
// Enabled on Confirmed.
type CustodyClosedMsg struct{ Confirmed bool }

type (
	custodyGatePassedMsg struct{}
	custodyGateCancelMsg struct{}
)

type Custody struct {
	kit        RecoveryKit
	handoff    CustodyHandoff
	deliveries []custodyOption
	step       custodyStep
	choice     int
	gate       Form
	copied     string // "", "password", or "kit"
	confirmed  bool
	// fromQR changes what the gate asks the user to type back from: the copy
	// on their phone, rather than what is still on the screen behind it.
	fromQR        bool
	handoffURL    string
	handoffErr    error
	width, height int
}

// NewCustody builds the flow. A chosen delivery skips the prompt, which is how
// --password-delivery selects without asking; CustodyDeliveryAsk offers the
// choice. A handoff of nil means no phone delivery is possible here.
func NewCustody(kit RecoveryKit, handoff CustodyHandoff, chosen CustodyDelivery) Custody {
	m := Custody{
		kit:        kit,
		handoff:    handoff,
		deliveries: custodyDeliveries(handoff),
		step:       custodyStepChoice,
	}
	m.gate = newCustodyGateForm(kit.Password)

	if chosen == CustodyDeliveryAsk && len(m.deliveries) == 1 {
		chosen = m.deliveries[0].delivery
	}
	for i, option := range m.deliveries {
		if option.delivery == chosen {
			m.choice = i
			m.step = m.stepFor(chosen)
		}
	}
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

	case custodyHandoffStartedMsg:
		m.handoffURL, m.handoffErr = msg.url, msg.err
		return m, nil

	case custodyGatePassedMsg:
		m.step = custodyStepDone
		m.confirmed = true
		return m, m.stopHandoff()

	case custodyGateCancelMsg:
		m.step = custodyStepSkipConfirm
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case MouseEvent:
		// Only the choice screen owns clicks of its own; everywhere else they
		// belong to the gate's form, below.
		if m.step != custodyStepChoice {
			break
		}
		if msg.IsClick {
			for i, option := range m.deliveries {
				if msg.Target == custodyDeliveryTarget(option.delivery) {
					m.choice = i
					return m.chooseDelivery()
				}
			}
		}
		return m, nil
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
	case custodyStepQRIntro:
		return m.qrIntroView()
	case custodyStepQR:
		return m.qrView()
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

func (m Custody) stepFor(delivery CustodyDelivery) custodyStep {
	if delivery == CustodyDeliveryQR {
		// The intro always precedes the code: the same-network requirement and
		// the certificate warning have to be read before anything is
		// scannable, and they do not fit on the screen the code needs.
		return custodyStepQRIntro
	}
	return custodyStepReveal
}

func (m Custody) chooseDelivery() (Custody, tea.Cmd) {
	m.step = m.stepFor(m.deliveries[m.choice].delivery)
	return m, nil
}

func (m Custody) startHandoff() tea.Cmd {
	if m.handoff == nil || m.handoffURL != "" {
		return nil
	}
	handoff, kit := m.handoff, m.kit
	return func() tea.Msg {
		url, err := handoff.Start(context.Background(), kit)
		return custodyHandoffStartedMsg{url: url, err: err}
	}
}

// stopHandoff takes the page down as soon as the flow is over. It is not the
// only teardown - the host tears down on any exit, and the session expires on
// its own - but it is the one that runs while the user is still watching.
func (m Custody) stopHandoff() tea.Cmd {
	if m.handoff == nil || m.handoffURL == "" {
		return nil
	}
	handoff := m.handoff
	return func() tea.Msg {
		_ = handoff.Stop(context.Background())
		return nil
	}
}

func (m Custody) handleKey(msg tea.KeyPressMsg) (Custody, tea.Cmd) {
	switch m.step {
	case custodyStepChoice:
		switch msg.String() {
		case "up", "k":
			m.choice = (m.choice + len(m.deliveries) - 1) % len(m.deliveries)
		case "down", "j", "tab":
			m.choice = (m.choice + 1) % len(m.deliveries)
		case "enter":
			return m.chooseDelivery()
		}
		return m, nil

	case custodyStepQRIntro:
		switch msg.String() {
		case "enter":
			m.step = custodyStepQR
			return m, m.startHandoff()
		case "esc":
			m.step = custodyStepChoice
		}
		return m, nil

	case custodyStepQR:
		switch msg.String() {
		case "enter":
			// Only reachable once there is something to have scanned.
			if m.handoffURL == "" {
				return m, nil
			}
			m.step = custodyStepGate
			m.fromQR = true
			return m, m.gate.Init()
		case "esc":
			m.step = custodyStepQRIntro
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
			return m, m.stopHandoff()
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
	for i, option := range m.deliveries {
		cursor, style := "  ", lipgloss.NewStyle()
		if i == m.choice {
			cursor = "> "
			style = style.Foreground(Colors.Focused).Bold(true)
		}
		lines = append(lines, mouse.Mark(custodyDeliveryTarget(option.delivery), style.Render(cursor+option.label)))
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

	lines := []string{lipgloss.NewStyle().Bold(true).Foreground(Colors.Primary).Render("ONCE RECOVERY KIT")}
	for _, row := range m.kit.rows() {
		lines = append(lines, label.Render(fmt.Sprintf("%-13s", row.label))+row.value)
	}

	return lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).BorderForeground(Colors.Primary).Padding(0, 2).
		Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

// qrIntroView is everything the user must know before anything is scannable.
// It is a screen of its own because the code needs the whole of the other one,
// and because a certificate warning nobody was warned about teaches exactly
// the habit self-hosting security depends on not having.
func (m Custody) qrIntroView() string {
	body := lipgloss.JoinVertical(lipgloss.Left,
		custodyMuted("The next screen shows a QR code. Scan it with your phone's camera:\n"+
			"it opens a page served by this machine, where your phone's password\n"+
			"manager can save the password directly."),
		"",
		custodyMuted(fmt.Sprintf("Your phone must be on the same network as this server - the page\n"+
			"is not reachable from anywhere else. The link works once, and\n"+
			"expires after %d minutes.", int(handoff.TTL.Minutes()))),
		"",
		lipgloss.NewStyle().Bold(true).Render("Your phone will warn about the certificate."),
		custodyMuted("Expected, this once: this server signs its own certificate and your\n"+
			"phone has never seen it. Tap through it for this one page - anywhere\n"+
			"else, that warning means stop. On iPhone the page often comes up\n"+
			"blank around the warning; pull down to refresh it."),
		custodyHelpLine("enter show the QR code", "esc back"))

	return custodyBox().Render(lipgloss.JoinVertical(lipgloss.Center,
		custodyTitle("Send it to your phone"), body))
}

// qrView is a screen of its own: the code plus its quiet zone takes most of
// the 24-row floor, which leaves no room to share with anything else.
func (m Custody) qrView() string {
	if m.handoffErr != nil {
		return custodyBox().Render(lipgloss.JoinVertical(lipgloss.Center,
			custodyTitle("Send it to your phone"),
			lipgloss.NewStyle().Foreground(Colors.Error).Render("Could not serve the page:"),
			custodyMuted(m.handoffErr.Error()),
			custodyHelpLine("esc back - show it on this screen instead")))
	}
	if m.handoffURL == "" {
		return custodyBox().Render(custodyMuted("Publishing the page..."))
	}

	code, err := QRCode(m.handoffURL)
	if err != nil {
		return custodyBox().Render(lipgloss.JoinVertical(lipgloss.Center,
			lipgloss.NewStyle().Foreground(Colors.Error).Render("Could not draw the code: "+err.Error()),
			custodyHelpLine("esc back")))
	}

	// No box and no margins: every row here belongs to the symbol.
	return lipgloss.JoinVertical(lipgloss.Center,
		code,
		custodyMuted(m.handoffURL),
		custodyMuted("enter once it is saved on your phone · esc back"))
}

func (m Custody) gateView() string {
	source := "what you just saved"
	if m.fromQR {
		source = "the copy on your phone"
	}
	body := lipgloss.JoinVertical(lipgloss.Left,
		custodyMuted("Type the password back from "+source+" to prove it is\n"+
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
