package ui

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/once/internal/docker"
	"github.com/basecamp/once/internal/logging"
	"github.com/basecamp/once/internal/metrics"
	"github.com/basecamp/once/internal/system"
	"github.com/basecamp/once/internal/userstats"
)

var dashboardShowDetails = true

var dashboardKeys = struct {
	Up        key.Binding
	Down      key.Binding
	Settings  key.Binding
	Actions   key.Binding
	NewApp    key.Binding
	Logs      key.Binding
	Details   key.Binding
	Tailscale key.Binding
	Backups   key.Binding
	Quit      key.Binding
}{
	Up:        WithHelp(NewKeyBinding("up", "k"), "↑/k", "up"),
	Down:      WithHelp(NewKeyBinding("down", "j"), "↓/j", "down"),
	Settings:  WithHelp(NewKeyBinding("s"), "s", "settings"),
	Actions:   WithHelp(NewKeyBinding("a"), "a", "actions"),
	NewApp:    WithHelp(NewKeyBinding("n"), "n", "new app"),
	Logs:      WithHelp(NewKeyBinding("g"), "g", "logs"),
	Details:   WithHelp(NewKeyBinding("d"), "d", "toggle details"),
	Tailscale: WithHelp(NewKeyBinding("t", "T"), "t", "tailscale"),
	Backups:   WithHelp(NewKeyBinding("b", "B"), "b", "backups"),
	Quit:      WithHelp(NewKeyBinding("esc"), "esc", "quit"),
}

type Dashboard struct {
	namespace     *docker.Namespace
	scraper       *metrics.MetricsScraper
	dockerScraper *docker.Scraper
	systemScraper *system.Scraper
	userStats     *userstats.Reader
	apps          []*docker.Application
	panels        []DashboardPanel
	header        DashboardHeader
	hostname      string
	selectedIndex int
	width, height int
	viewport      viewport.Model
	toggling      bool
	togglingApp   string
	progress      Progress
	help          Help
	overlay       Component

	tailscaleEnabled bool
	tailnetSuffix    string // tailnet MagicDNS suffix, for computing app service URLs
	tailnetNodeHost  string // sidecar node hostname, for computing Funnel URLs

	// tailnetWarnings caches the warn-only note for each service URL that does
	// not resolve from this machine, keyed by URL and refreshed with the rest
	// of the Tailscale info. A URL that is absent has either resolved or not
	// been checked yet, and neither renders a warning.
	tailnetWarnings map[string]string

	// backupStatus caches the install-wide backup facts, refreshed on its own
	// slow schedule because the staleness answer costs a restic container per
	// app. The zero value is "not configured", which renders nothing.
	backupStatus docker.BackupStatus
}

type dashboardTickMsg struct{}

type tailscaleInfoMsg struct {
	enabled  bool
	suffix   string
	nodeHost string
	warnings map[string]string
}

type tailscaleRefreshMsg struct{}

type startStopFinishedMsg struct {
	err error
}

func NewDashboard(ns *docker.Namespace, apps []*docker.Application, selectedIndex int,
	scraper *metrics.MetricsScraper, dockerScraper *docker.Scraper, systemScraper *system.Scraper, userStats *userstats.Reader,
) Dashboard {
	vp := viewport.New()
	vp.MouseWheelEnabled = false
	vp.KeyMap = viewport.KeyMap{} // disable default keys, we handle navigation ourselves

	hostname, _ := os.Hostname()

	d := Dashboard{
		namespace:     ns,
		scraper:       scraper,
		dockerScraper: dockerScraper,
		systemScraper: systemScraper,
		userStats:     userStats,
		apps:          apps,
		selectedIndex: selectedIndex,
		viewport:      vp,
		header:        NewDashboardHeader(systemScraper),
		hostname:      hostname,
		progress:      NewProgress(0, Colors.Border),
		help:          NewHelp(),
	}
	d.buildPanels()
	d.help.SetBindings(d.helpBindings())
	return d
}

// ShowingSecret propagates the overlay's mouse-tracking opt-out (the custody
// moment inside the Backups overlay shows the repo password).
func (m Dashboard) ShowingSecret() bool {
	return overlayShowingSecret(m.overlay)
}

// Busy propagates the overlay's quit guard: the Backups overlay holds one
// while the phone handoff is published, because its route and certificate
// outlive the process that made them.
func (m Dashboard) Busy() bool {
	return overlayBusy(m.overlay)
}

func (m Dashboard) Init() tea.Cmd {
	return tea.Batch(m.scheduleNextDashboardTick(), m.fetchTailscaleInfo(), m.scheduleTailscaleRefresh(),
		m.fetchBackupStatus(), m.scheduleBackupStatusRefresh())
}

func (m Dashboard) Update(msg tea.Msg) (Component, tea.Cmd) {
	var cmds []tea.Cmd

	if m.overlay != nil {
		var cmd tea.Cmd
		m.overlay, cmd = m.overlay.Update(msg)
		cmds = append(cmds, cmd)

		switch msg.(type) {
		case tea.KeyPressMsg, MouseEvent:
			return m, cmd
		}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.progress = m.progress.SetWidth(m.width)
		m.help.SetWidth(m.width)
		m.updateViewportSize()
		m.rebuildViewportContent()

	case MouseEvent:
		if msg.IsClick {
			if i, ok := m.panelIndexAtY(msg.Y); ok {
				m.selectPanel(i)
				return m, nil
			}
			var cmd tea.Cmd
			m.help, cmd = m.help.Update(msg)
			return m, cmd
		}

	case tea.KeyPressMsg:
		if key.Matches(msg, dashboardKeys.Quit) {
			return m, func() tea.Msg { return QuitMsg{} }
		}
		if key.Matches(msg, dashboardKeys.Up) {
			m.selectPanel(m.selectedIndex - 1)
			return m, nil
		}
		if key.Matches(msg, dashboardKeys.Down) {
			m.selectPanel(m.selectedIndex + 1)
			return m, nil
		}
		if key.Matches(msg, dashboardKeys.NewApp) {
			return m, func() tea.Msg { return NavigateToInstallMsg{} }
		}
		if key.Matches(msg, dashboardKeys.Settings) && len(m.apps) > 0 {
			return m, m.openOverlay(NewSettingsMenu(m.apps[m.selectedIndex]))
		}
		if key.Matches(msg, dashboardKeys.Actions) && len(m.apps) > 0 && !m.toggling {
			return m, m.openOverlay(NewActionsMenu(m.apps[m.selectedIndex]))
		}
		if key.Matches(msg, dashboardKeys.Logs) && len(m.apps) > 0 {
			return m, func() tea.Msg { return NavigateToLogsMsg{App: m.apps[m.selectedIndex]} }
		}
		if key.Matches(msg, dashboardKeys.Tailscale) {
			return m, m.openOverlay(NewTailscaleForm(m.namespace))
		}
		if key.Matches(msg, dashboardKeys.Backups) {
			return m, m.openOverlay(NewBackupsForm(m.namespace))
		}
		if key.Matches(msg, dashboardKeys.Details) && len(m.apps) > 0 {
			dashboardShowDetails = !dashboardShowDetails
			m.updateViewportSize()
			m.selectPanel(m.selectedIndex)
			return m, nil
		}

	case SettingsMenuCloseMsg:
		m.overlay = nil

	case TailscaleFormCloseMsg:
		m.overlay = nil

	case BackupsFormCloseMsg:
		m.overlay = nil

	case SettingsMenuSelectMsg:
		m.overlay = nil
		return m, func() tea.Msg {
			return NavigateToSettingsSectionMsg{App: msg.app, Section: msg.section}
		}

	case ActionsMenuCloseMsg:
		m.overlay = nil

	case ActionsMenuSelectMsg:
		m.overlay = nil
		switch msg.action {
		case ActionsMenuStartStop:
			app := msg.app
			m.toggling = true
			m.togglingApp = app.Settings.Name
			m.progress = NewProgress(m.width, Colors.Border)
			m.updateViewportSize()
			m.rebuildViewportContent()
			return m, tea.Batch(m.progress.Init(), m.runStartStop(app))
		case ActionsMenuRemove:
			return m, func() tea.Msg { return NavigateToRemoveMsg{App: msg.app} }
		}

	case startStopFinishedMsg:
		m.toggling = false
		m.togglingApp = ""
		m.updateViewportSize()
		m.rebuildViewportContent()

	case scrapeDoneMsg:
		m.rebuildViewportContent()

	case dashboardTickMsg:
		m.rebuildViewportContent()
		cmds = append(cmds, m.scheduleNextDashboardTick())

	case tailscaleInfoMsg:
		m.tailscaleEnabled = msg.enabled
		m.tailnetSuffix = msg.suffix
		m.tailnetNodeHost = msg.nodeHost
		m.tailnetWarnings = msg.warnings
		m.updateViewportSize()
		m.rebuildViewportContent()

	case tailscaleRefreshMsg:
		cmds = append(cmds, m.fetchTailscaleInfo(), m.scheduleTailscaleRefresh())

	case backupStatusMsg:
		m.backupStatus = msg.status
		m.updateViewportSize()
		m.rebuildViewportContent()

	case backupStatusRefreshMsg:
		cmds = append(cmds, m.fetchBackupStatus(), m.scheduleBackupStatusRefresh())

	case ProgressTickMsg:
		if m.toggling {
			var cmd tea.Cmd
			m.progress, cmd = m.progress.Update(msg)
			cmds = append(cmds, cmd)
		}

	case namespaceChangedMsg:
		previousName := ""
		if m.selectedIndex < len(m.apps) {
			previousName = m.apps[m.selectedIndex].Settings.Name
		}
		m.apps = m.namespace.Applications()
		m.buildPanels()
		newIndex := 0
		for i, app := range m.apps {
			if app.Settings.Name == previousName {
				newIndex = i
				break
			}
		}
		m.selectPanel(newIndex)
		m.help.SetBindings(m.helpBindings())
	}

	return m, tea.Batch(cmds...)
}

func (m Dashboard) View() string {
	titleLine := Styles.TitleRule(m.width, m.hostname)

	helpView := m.help.View()
	if hint := verboseLogHint(); hint != "" {
		helpView += lipgloss.NewStyle().Foreground(Colors.Border).Render(" • " + hint)
	}
	helpLine := Styles.CenteredLine(m.width, helpView)

	headerView := m.header.View(m.width)

	if len(m.apps) == 0 {
		emptyMsg := lipgloss.NewStyle().Foreground(Colors.Border).Render("There are no applications installed")
		headerH := m.header.Height(m.width)
		if headerH > 0 {
			headerH += 2
		}
		middleHeight := m.height - 1 - headerH - 1 // title + header + help
		centeredContent := lipgloss.Place(m.width, middleHeight, lipgloss.Center, lipgloss.Center, emptyMsg)
		if headerView != "" {
			return titleLine + "\n\n" + headerView + "\n" + centeredContent + "\n" + helpLine
		}
		return titleLine + "\n" + centeredContent + "\n" + helpLine
	}

	var parts []string
	parts = append(parts, titleLine)
	if headerView != "" {
		parts = append(parts, "", headerView, "")
	}
	parts = append(parts, m.viewport.View())
	if m.toggling {
		parts = append(parts, m.progress.View())
	}
	if note := m.tailnetWarningNote(); note != "" {
		parts = append(parts, note)
	}
	if note := m.backupNote(); note != "" {
		parts = append(parts, note)
	}
	parts = append(parts, helpLine)
	content := strings.Join(parts, "\n")

	if m.overlay != nil {
		return OverlayCenter(content, m.overlay.View(), m.width, m.height)
	}

	return content
}

// Private

// openOverlay mounts an overlay sized to the dashboard and starts it.
func (m *Dashboard) openOverlay(overlay Component) tea.Cmd {
	m.overlay = overlay
	var cmd tea.Cmd
	m.overlay, cmd = m.overlay.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	return tea.Batch(m.overlay.Init(), cmd)
}

func (m Dashboard) helpBindings() []key.Binding {
	if len(m.apps) > 0 {
		return []key.Binding{
			dashboardKeys.Up, dashboardKeys.Down, dashboardKeys.Actions,
			dashboardKeys.Settings, dashboardKeys.Logs, dashboardKeys.Details, dashboardKeys.NewApp, dashboardKeys.Tailscale, dashboardKeys.Backups, dashboardKeys.Quit,
		}
	}
	return []key.Binding{dashboardKeys.NewApp, dashboardKeys.Tailscale, dashboardKeys.Backups, dashboardKeys.Quit}
}

// tailnetURL is the app's service URL: computed from the stored Magic DNS
// suffix, never looked up live. Empty when the app has no tailnet service.
func (m Dashboard) tailnetURL(app docker.ApplicationSettings) string {
	if app.Exposure != docker.ExposureTailnet {
		return ""
	}
	return (docker.TailscaleSettings{DomainSuffix: m.tailnetSuffix}).TailnetURL(app.Host)
}

// tailnetWarningNote spells out, below the panels, what the selected panel's
// Tailnet URL marker means: the row itself has no room for the sentence.
func (m Dashboard) tailnetWarningNote() string {
	if m.selectedIndex >= len(m.panels) {
		return ""
	}
	warning := m.tailnetWarnings[m.tailnetURL(m.panels[m.selectedIndex].app.Settings)]
	if warning == "" {
		return ""
	}
	wrapped := lipgloss.NewStyle().Foreground(Colors.Warning).Width(max(m.width-4, 20)).Render("⚠ " + warning)
	return lipgloss.PlaceHorizontal(m.width, lipgloss.Center, wrapped)
}

// resolvableTailnetHosts is the hostnames worth a DNS check: a stopped app's
// service is deregistered and a non-tailnet app has no service at all.
func (m Dashboard) resolvableTailnetHosts() []string {
	var hosts []string
	for _, app := range m.apps {
		if app.Running && app.Settings.Exposure == docker.ExposureTailnet {
			hosts = append(hosts, app.Settings.Host)
		}
	}
	return hosts
}

func (m Dashboard) runStartStop(app *docker.Application) tea.Cmd {
	return func() tea.Msg {
		var err error
		if app.Running {
			err = app.Stop(context.Background())
		} else {
			err = app.Start(context.Background())
		}
		return startStopFinishedMsg{err: err}
	}
}

func (m Dashboard) scheduleNextDashboardTick() tea.Cmd {
	return tea.Every(time.Second, func(time.Time) tea.Msg { return dashboardTickMsg{} })
}

func (m Dashboard) scheduleTailscaleRefresh() tea.Cmd {
	return tea.Every(5*time.Second, func(time.Time) tea.Msg { return tailscaleRefreshMsg{} })
}

// fetchTailscaleInfo asynchronously reads the global enabled state, the stored
// Magic DNS suffix, and whether each service name resolves from here, so the
// details card can stay current without blocking the UI. Failures degrade to
// "not enabled".
func (m Dashboard) fetchTailscaleInfo() tea.Cmd {
	ns := m.namespace
	hosts := m.resolvableTailnetHosts()
	return func() tea.Msg {
		ctx := context.Background()
		enabled, err := ns.Tailscale().Enabled(ctx)
		if err != nil || !enabled {
			return tailscaleInfoMsg{enabled: false}
		}
		suffix, _ := ns.Tailscale().DomainSuffix(ctx)
		settings, _ := ns.Tailscale().LoadSettings(ctx)
		return tailscaleInfoMsg{
			enabled:  true,
			suffix:   suffix,
			nodeHost: settings.NodeHost,
			warnings: tailnetDNSWarnings(ctx, hosts, suffix),
		}
	}
}

func (m *Dashboard) selectPanel(index int) {
	m.selectedIndex = max(0, min(index, len(m.apps)-1))
	m.updateViewportSize()
	m.rebuildViewportContent()
	m.scrollToSelection()
}

func (m *Dashboard) updateViewportSize() {
	titleHeight := 1
	headerHeight := m.header.Height(m.width)
	if headerHeight > 0 {
		headerHeight += 2 // blank lines above and below header
	}
	helpHeight := m.help.Height()
	progressHeight := 0
	if m.toggling {
		progressHeight = 1
	}
	noteHeight := 0
	if note := m.tailnetWarningNote(); note != "" {
		noteHeight = lipgloss.Height(note)
	}
	if note := m.backupNote(); note != "" {
		noteHeight += lipgloss.Height(note)
	}
	vpHeight := max(m.height-titleHeight-headerHeight-helpHeight-progressHeight-noteHeight, 0)
	m.viewport.SetHeight(vpHeight)
	m.viewport.SetWidth(m.width)
}

func (m *Dashboard) rebuildViewportContent() {
	scales := m.computeScales()
	var views []string
	for i := range m.panels {
		m.panels[i].tailscaleEnabled = m.tailscaleEnabled
		app := m.panels[i].app.Settings
		url := m.tailnetURL(app)
		var funnelURL string
		if app.Exposure == docker.ExposureTailnet {
			funnelURL = (docker.TailscaleSettings{NodeHost: m.tailnetNodeHost}).FunnelURL(app.ActiveFunnelPort())
		}
		m.panels[i].tailnetURL = url
		m.panels[i].funnelURL = funnelURL
		m.panels[i].tailnetWarning = m.tailnetWarnings[url]
		toggling := m.toggling && m.togglingApp == m.panels[i].app.Settings.Name
		views = append(views, m.panels[i].View(i == m.selectedIndex, toggling, dashboardShowDetails, m.width, scales))
	}
	m.viewport.SetContent(lipgloss.JoinVertical(lipgloss.Left, views...))
}

func (m *Dashboard) computeScales() DashboardScales {
	var maxTraffic float64
	for i := range m.panels {
		traffic := m.panels[i].DataMaxes()
		maxTraffic = max(maxTraffic, traffic)
	}
	return DashboardScales{
		CPU:     ChartScale{max: float64(m.systemScraper.NumCPUs()) * 100},
		Memory:  ChartScale{max: float64(m.systemScraper.MemTotal())},
		Traffic: NewChartScale(UnitCount, maxTraffic),
	}
}

func (m *Dashboard) scrollToSelection() {
	if len(m.panels) == 0 {
		return
	}

	panelTop := 0
	for i := range m.selectedIndex {
		panelTop += m.panels[i].Height(dashboardShowDetails)
	}
	panelBottom := panelTop + m.panels[m.selectedIndex].Height(dashboardShowDetails)
	if panelTop < m.viewport.YOffset() {
		m.viewport.SetYOffset(panelTop)
	} else if panelBottom > m.viewport.YOffset()+m.viewport.Height() {
		m.viewport.SetYOffset(panelBottom - m.viewport.Height())
	}
}

func (m *Dashboard) panelIndexAtY(y int) (int, bool) {
	titleHeight := 1
	headerHeight := m.header.Height(m.width)
	if headerHeight > 0 {
		headerHeight += 2
	}
	vpRow := y - titleHeight - headerHeight
	if vpRow < 0 || vpRow >= m.viewport.Height() {
		return 0, false
	}

	contentRow := vpRow + m.viewport.YOffset()
	top := 0
	for i := range m.panels {
		h := m.panels[i].Height(dashboardShowDetails)
		if contentRow < top+h {
			return i, true
		}
		top += h
	}
	return 0, false
}

func (m *Dashboard) buildPanels() {
	m.panels = make([]DashboardPanel, len(m.apps))
	for i, app := range m.apps {
		m.panels[i] = NewDashboardPanel(app, m.scraper, m.dockerScraper, m.userStats)
	}
}

// Helpers

// tailnetDNSWarnings resolves each host's service URL and returns the warn-only
// note for the ones that do not resolve from this machine, keyed by URL. It runs
// off the UI goroutine, in parallel, so a wedged resolver costs one timeout
// rather than one per app - and never a frame.
func tailnetDNSWarnings(ctx context.Context, hosts []string, suffix string) map[string]string {
	settings := docker.TailscaleSettings{DomainSuffix: suffix}
	urls := make([]string, len(hosts))
	notes := make([]string, len(hosts))
	var wg sync.WaitGroup
	for i, host := range hosts {
		urls[i] = settings.TailnetURL(host)
		wg.Go(func() { notes[i] = docker.TailnetDNSWarning(ctx, urls[i]) })
	}
	wg.Wait()

	warnings := map[string]string{}
	for i, note := range notes {
		if note != "" {
			warnings[urls[i]] = note
		}
	}
	return warnings
}

func verboseLogHint() string {
	if !logging.Verbose() {
		return ""
	}
	path, err := logging.LogFilePath()
	if err != nil {
		return ""
	}
	return "verbose log: " + path
}
