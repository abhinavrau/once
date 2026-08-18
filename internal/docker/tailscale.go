package docker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// TailscaleSettings.Mode values: where the tailscaled that backs Docktail
// lives, so status and disable know what Once manages. The sidecar is the
// default; host mode is explicit opt-in (ADR-0007). Mode exists only under
// the Services topology - resolving and enforcing that belongs to the
// Services adapter (ADR-0015); the constants and the shape check stay here
// because TailscaleSettings.Mode is documented and validated regardless of
// topology.
const (
	TailscaleModeHost    = "host"
	TailscaleModeSidecar = "sidecar"
)

// ValidateTailscaleMode rejects a requested mode other than host, sidecar, or
// empty (empty keeps an already-enabled install's stored mode and defaults a
// fresh enable to the sidecar).
func ValidateTailscaleMode(mode string) error {
	switch mode {
	case "", TailscaleModeHost, TailscaleModeSidecar:
		return nil
	}
	return fmt.Errorf("invalid Tailscale mode %q: must be %q or %q", mode, TailscaleModeHost, TailscaleModeSidecar)
}

// Funnel durations: a short default so apps don't stay public by accident, and
// a hard 24h cap (see PRD "Out of Scope").
const (
	DefaultFunnelDuration = 10 * time.Minute
	MaxFunnelDuration     = 24 * time.Hour
)

// funnelActivation bounds how long we wait for the local tailscaled to report
// a Funnel active before surfacing a failure. ponytail: fixed poll, fine for an
// interactive enable; lengthen if first-time activation proves slower.
const (
	funnelActivationTimeout = 15 * time.Second
	funnelActivationPoll    = time.Second
)

var (
	ErrFunnelDurationInvalid = errors.New("funnel duration must be positive")
	ErrFunnelDurationTooLong = fmt.Errorf("funnel duration must not exceed %s", MaxFunnelDuration)
	ErrFunnelPortsExhausted  = errors.New("all Funnel public ports (443, 8443, 10000) are in use; disable another app's Funnel first")
)

// ValidateFunnelDuration enforces the positive/≤24h bounds on a requested
// Funnel duration.
func ValidateFunnelDuration(d time.Duration) error {
	if d <= 0 {
		return ErrFunnelDurationInvalid
	}
	if d > MaxFunnelDuration {
		return ErrFunnelDurationTooLong
	}
	return nil
}

// Hidden control-server seam: when these env vars are set, the tailnet
// provider joins controlUrl with the pre-minted authKey instead of Once
// minting one via OAuth, and runs without cloud credentials (manual mode). No
// CLI/TUI exposes them - they exist to point at the headscale test harness,
// and are promotable to first-class headscale support later. Shared by every
// topology, so the accessor lives in the facade rather than any one adapter.
const (
	envControlURL = "ONCE_TAILSCALE_CONTROL_URL"
	envAuthKey    = "ONCE_TAILSCALE_AUTH_KEY"
)

// removedTailscaleFields are label fields older versions of Once wrote that no
// longer exist: the TSDProxy-era apiKey. Shed on rewrite, unlike unknown fields
// from newer versions, which are preserved - a dead credential must not ride
// along in every label Once writes from here on.
var removedTailscaleFields = []string{"apiKey"}

type TailscaleSettings struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	// Tag is the Tailscale tag (e.g. tag:once) the sidecar advertises and
	// docktail stamps on the Service definitions it creates. A tagged OAuth
	// client can only mint keys for tags it owns, so this must be a tag the
	// client owns in the tailnet ACL. It is stored on the headscale control seam
	// too, where only the host-mode pre-flight reads it: that path mints no
	// tagged keys and runs docktail without the cloud API.
	Tag string `json:"tag,omitempty"`
	// DomainSuffix is the tailnet's MagicDNS base (e.g. tailnet-name.ts.net),
	// read from the local tailscaled at enable time. With it stored, app tailnet
	// URLs are computed without any live lookup.
	DomainSuffix string `json:"domainSuffix,omitempty"`
	// NodeHost is the sidecar node's own tailnet hostname (e.g.
	// once-tailscale-1.tailnet-name.ts.net), learned the first time a Funnel
	// activates. Funnel is per-node, so every app's Funnel URL is this host
	// with the app's public port - stored so those URLs need no live lookup.
	// Services-topology only; ticket 04's nodes topology derives a Funnel URL
	// from the app's own tailnet URL instead.
	NodeHost string `json:"nodeHost,omitempty"`
	// Mode records whether Docktail drives a tailscaled already on the host
	// (TailscaleModeHost) or the Once-managed once-tailscale sidecar
	// (TailscaleModeSidecar). Services-topology only.
	Mode string `json:"mode,omitempty"`
	// Topology is the Tailnet Topology (ADR-0015): TailnetTopologyServices or
	// TailnetTopologyNodes. Always written explicitly - a label with no
	// topology predates ADR-0015, and every read path refuses it rather than
	// guessing.
	Topology string `json:"topology"`

	labelExtra
}

// TailnetURL builds an app's https://<app>.<suffix> tailnet URL from the stored
// domain suffix, or "" when the suffix isn't known yet.
func (s TailscaleSettings) TailnetURL(appName string) string {
	if s.DomainSuffix == "" {
		return ""
	}
	return "https://" + appName + "." + s.DomainSuffix
}

// FunnelURL builds the public URL a Funnel on the given public port serves,
// from the stored node hostname, or "" when the node host isn't known yet.
// Funnel is per-node: all apps share this host and differ only by port.
// Services-topology shape only; ticket 04/05 give the Nodes topology its own
// call path once a frontend needs to render it (nodes has no NodeHost).
func (s TailscaleSettings) FunnelURL(port int) string {
	if s.NodeHost == "" || port == 0 {
		return ""
	}
	return funnelURL(s.NodeHost + ":" + strconv.Itoa(port))
}

func UnmarshalTailscaleSettings(s string) (TailscaleSettings, error) {
	return decodeLabel[TailscaleSettings](s, removedTailscaleFields...)
}

func (s TailscaleSettings) Marshal() string {
	return encodeLabel(s)
}

// Private

// normalizeTag prepends "tag:" to each comma-separated segment that lacks it, so
// a bare name like "once" becomes "tag:once" before it reaches the Tailscale API
// or the docktail config. Already-prefixed segments pass through unchanged. A tag
// that is empty (or only whitespace) collapses to "" so the downstream "a tag is
// required" check still fires rather than a blank tag reaching the API.
func (s TailscaleSettings) normalizeTag() string {
	if strings.TrimSpace(s.Tag) == "" {
		return ""
	}
	segments := strings.Split(s.Tag, ",")
	for i, seg := range segments {
		seg = strings.TrimSpace(seg)
		if !strings.HasPrefix(seg, "tag:") {
			seg = "tag:" + seg
		}
		segments[i] = seg
	}
	return strings.Join(segments, ",")
}

// suppressUnusedCredentials blanks the OAuth credentials when the hidden
// control seam is active: that path joins with a pre-minted auth key and runs
// without the cloud API, so nothing reads them and whatever the user typed
// must not reach the once label. The tag is deliberately kept: the host-mode
// pre-flight still checks it, and both frontends have always stored it.
func (s TailscaleSettings) suppressUnusedCredentials() TailscaleSettings {
	if controlSeamActive() {
		s.ClientID, s.ClientSecret = "", ""
	}
	return s
}

// Tailscale is the facade over the Tailnet Topology seam (ADR-0015): exactly
// one topology adapter is active per install, chosen by the settings' stored
// (or freshly resolved) Topology. The facade stays concrete and deep -
// settings on the active provider container's label, enabled-by-existence,
// rebuild-on-change, the retrofit Roll, bounded Funnel duration, suffix and
// node-host persistence, and the Control Seam accessor - while everything
// that varies per topology (boot/remove/destroy, the app label dialect, the
// credential probe, the Tailnet Reader, verify, status, Funnel port
// allocation, host-name validation, and the static frontend facts) lives in
// one adapter each, behind the private tailnetTopology interface.
type Tailscale struct {
	namespace    *Namespace
	Settings     *TailscaleSettings
	domainSuffix string // in-memory cache so a learned suffix survives a failed persist
}

func NewTailscale(ns *Namespace) *Tailscale {
	return &Tailscale{namespace: ns}
}

// Enable idempotently brings up the topology described by settings (already
// resolved: settings.Topology and settings.Mode carry the effective values,
// not a request - Namespace.EnableTailscale does that resolution). Re-running
// with unchanged credentials, tag, mode, and topology just ensures everything
// is running (the supporting-container doctrine) and does not roll apps;
// anything in that set changing rebuilds the topology from scratch (each
// topology's state volume is kept, since boot/remove never touch it) and
// rolls every running app so it picks up the new topology's labels
// immediately - which is also how a stored label carrying no topology (or no
// mode) self-heals, since the resolved values differ from the stored ones.
func (t *Tailscale) Enable(ctx context.Context, settings TailscaleSettings) error {
	existing, found, err := t.loadStoredSettings(ctx)
	if err != nil {
		return err
	}

	if found && sameTopologyRequest(existing, settings) {
		topology, err := t.topologyFor(existing.Topology)
		if err != nil {
			return err
		}
		if err := topology.boot(ctx, existing); err != nil {
			return err
		}
		t.Settings = &existing
		return nil
	}

	if found {
		// The rebuild recreates the label from the caller's settings, which
		// carry no unknown fields, so hand them the stored label's: known
		// optional fields (suffix, node host) are deliberately dropped
		// because the tailnet may differ, but a field only a newer Once
		// models is not this binary's to judge - stale-but-preserved beats
		// destroyed (ADR-0008).
		settings.labelExtra = existing.labelExtra
		oldTopology, err := t.topologyFor(activeTopologyName(existing))
		if err != nil {
			return err
		}
		if err := oldTopology.remove(ctx); err != nil {
			return err
		}
	}

	newTopology, err := t.topologyFor(settings.Topology)
	if err != nil {
		return err
	}
	if err := newTopology.boot(ctx, settings); err != nil {
		return err
	}
	t.Settings = &settings

	// Retrofit running apps so they appear on the tailnet immediately, not
	// just on their next deploy.
	return t.namespace.RollApplications(ctx)
}

// Disable removes the active topology's containers, keeping any state volume
// that carries node identity, so a later re-enable doesn't churn Magic DNS.
func (t *Tailscale) Disable(ctx context.Context) error {
	settings, found, err := t.loadStoredSettings(ctx)
	if err != nil {
		return err
	}
	if found {
		topology, err := t.topologyFor(activeTopologyName(settings))
		if err != nil {
			return err
		}
		if err := topology.remove(ctx); err != nil {
			return err
		}
	}
	t.Settings = nil

	// Roll running apps to strip the tailnet labels - the provider container
	// is gone, so Enabled() is now false and the roll recreates them without
	// the labels.
	return t.namespace.RollApplications(ctx)
}

// Enabled is implicit in the existence of a provider container (the same
// doctrine the proxy follows) - whichever topology's, since exactly one is
// active per install.
func (t *Tailscale) Enabled(ctx context.Context) (bool, error) {
	_, found, err := t.loadStoredSettings(ctx)
	return found, err
}

// LoadSettings reads the Tailscale settings stored on the active provider
// container's label, for pre-populating UIs and every read path. Errors if
// Tailscale isn't enabled, or if the stored label predates ADR-0015 (no
// topology) - re-running `once tailscale enable` self-heals that.
func (t *Tailscale) LoadSettings(ctx context.Context) (TailscaleSettings, error) {
	settings, found, err := t.loadStoredSettings(ctx)
	if err != nil {
		return TailscaleSettings{}, err
	}
	if !found {
		return TailscaleSettings{}, errors.New("Tailscale is not enabled")
	}
	if settings.Topology == "" {
		return TailscaleSettings{}, errOlderOnceSetup
	}
	return settings, nil
}

// DomainSuffix returns the tailnet's MagicDNS suffix (e.g. tailnet-name.ts.net).
// It prefers the value persisted in settings; otherwise it reads the suffix
// from the active topology's reader and persists it, so enables that raced a
// slow control plane self-heal. Returns "" when the daemon hasn't joined the
// tailnet yet, so the suffix can't be known.
func (t *Tailscale) DomainSuffix(ctx context.Context) (string, error) {
	if t.domainSuffix != "" {
		return t.domainSuffix, nil
	}

	settings, reader, err := t.session(ctx)
	if err != nil {
		return "", err
	}
	if settings.DomainSuffix != "" {
		t.domainSuffix = settings.DomainSuffix
		return settings.DomainSuffix, nil
	}

	suffix, err := reader.MagicDNSSuffix(ctx)
	if err != nil || suffix == "" {
		return "", err
	}
	// ponytail: Docker labels are immutable, so baking the suffix into the
	// settings label means one cheap recreate. Cache the suffix first so a
	// failed persist still short-circuits here next time rather than
	// recreating on every poll.
	t.domainSuffix = suffix
	settings.DomainSuffix = suffix
	_ = t.persistSettings(ctx, settings)
	return suffix, nil
}

// WaitForFunnelActive polls the active topology's reader until it reports a
// Funnel serving the app with this Host - the name it is advertised under -
// then returns the funnel's public URL. Funnel also needs the tailnet ACL's
// funnel node attribute, which Once can't manage - surfacing activation
// failure prevents reporting a Funnel active when it isn't.
func (t *Tailscale) WaitForFunnelActive(ctx context.Context, host string) (string, error) {
	settings, reader, err := t.session(ctx)
	if err != nil {
		return "", err
	}
	topology, err := t.topologyFor(settings.Topology)
	if err != nil {
		return "", err
	}
	hostPort, err := waitForFunnel(ctx, reader, host, topology.logsHint(), funnelActivationTimeout, funnelActivationPoll)
	if err != nil {
		return "", err
	}
	// Persist the node's hostname (write-through, like DomainSuffix) so status
	// and the TUI can render every Funnel URL without a live serve read.
	if host, _, ok := strings.Cut(hostPort, ":"); ok && host != settings.NodeHost {
		settings.NodeHost = host
		_ = t.persistSettings(ctx, settings)
	}
	return funnelURL(hostPort), nil
}

// FunnelPort allocates self's Funnel public port under the active topology,
// or (0, nil) for a topology that needs no allocation (ticket 04: nodes).
func (t *Tailscale) FunnelPort(ctx context.Context, self *Application) (int, error) {
	settings, err := t.LoadSettings(ctx)
	if err != nil {
		return 0, err
	}
	topology, err := t.topologyFor(settings.Topology)
	if err != nil {
		return 0, err
	}
	return topology.funnelPort(self, self.namespace.applications)
}

// AppLabels returns the labels that expose an app under the active topology,
// or nil when Tailscale is disabled. enabled is the caller's already-resolved
// Tailscale.Enabled(ctx) result, so a disabled install costs no extra
// inspect. host is the app's Host (the service or node name); network is the
// namespace's Docker network; funnelPort is the app's active Funnel public
// port, or 0.
func (t *Tailscale) AppLabels(ctx context.Context, enabled bool, host, network string, funnelPort int) (map[string]string, error) {
	if !enabled {
		return nil, nil
	}
	settings, err := t.LoadSettings(ctx)
	if err != nil {
		return nil, err
	}
	topology, err := t.topologyFor(settings.Topology)
	if err != nil {
		return nil, err
	}
	return topology.appLabels(host, network, funnelPort), nil
}

// ShadowWarnings reports, for each service name, whether an existing tailnet
// machine's Magic DNS entry shadows it: machine names win over Services at
// the same FQDN, so the app URL would silently resolve to the machine.
// Best effort by design - this guards a warning, so an unreadable daemon
// (or Tailscale not enabled) yields no warnings rather than failing the
// enable or deploy that asked.
func (t *Tailscale) ShadowWarnings(ctx context.Context, names []string) []string {
	_, reader, err := t.session(ctx)
	if err != nil {
		return nil
	}
	return shadowWarnings(ctx, reader, names)
}

// Verify confirms each expected app is reachable-in-principle after enable,
// under the active topology.
func (t *Tailscale) Verify(ctx context.Context, expected []string, progress DeployProgressCallback) error {
	if len(expected) == 0 {
		return nil
	}
	settings, topology, err := t.sessionTopology(ctx)
	if err != nil {
		return err
	}
	return topology.verify(ctx, settings, expected, progress)
}

// ServiceStatuses reports each named app's current tailnet state, under the
// active topology.
func (t *Tailscale) ServiceStatuses(ctx context.Context, names []string) ([]ServiceStatus, error) {
	settings, topology, err := t.sessionTopology(ctx)
	if err != nil {
		return nil, err
	}
	return topology.status(ctx, settings, names)
}

// Destroy removes the active topology's containers and its state volume
// (full cleanup, used by teardown - unlike Disable, which retains state).
func (t *Tailscale) Destroy(ctx context.Context) error {
	settings, found, err := t.loadStoredSettings(ctx)
	if err != nil {
		return err
	}
	if found {
		topology, err := t.topologyFor(activeTopologyName(settings))
		if err != nil {
			return err
		}
		if err := topology.destroy(ctx); err != nil {
			return err
		}
	}
	t.Settings = nil
	return nil
}

// Private

// session binds every read path's stored settings to the reader the active
// topology implies: the single door (ADR-0010) that replaces a hand-written
// LoadSettings-plus-adapter-selection pair at each read path's start.
func (t *Tailscale) session(ctx context.Context) (TailscaleSettings, TailnetReader, error) {
	settings, topology, err := t.sessionTopology(ctx)
	if err != nil {
		return TailscaleSettings{}, nil, err
	}
	return settings, topology.reader(settings), nil
}

// sessionTopology is session's sibling for read paths that need the adapter
// itself (Verify, ServiceStatuses), not just the reader it hands out.
func (t *Tailscale) sessionTopology(ctx context.Context) (TailscaleSettings, tailnetTopology, error) {
	settings, err := t.LoadSettings(ctx)
	if err != nil {
		return TailscaleSettings{}, nil, err
	}
	topology, err := t.topologyFor(settings.Topology)
	if err != nil {
		return TailscaleSettings{}, nil, err
	}
	return settings, topology, nil
}

// persistSettings rewrites the active provider container's settings label via
// the topology's cheap relabel operation. Used to bake in a newly-learned
// domain suffix or node host.
func (t *Tailscale) persistSettings(ctx context.Context, settings TailscaleSettings) error {
	topology, err := t.topologyFor(settings.Topology)
	if err != nil {
		return err
	}
	if err := topology.relabel(ctx, settings); err != nil {
		return err
	}
	t.Settings = &settings
	return nil
}

// Helpers

// sameTopologyRequest reports whether a fresh enable request matches what is
// already stored: everything that, if it changed, must rebuild the topology
// (credentials, tag, mode, and - since ADR-0015 - the topology itself).
func sameTopologyRequest(existing, requested TailscaleSettings) bool {
	return existing.ClientID == requested.ClientID &&
		existing.ClientSecret == requested.ClientSecret &&
		existing.Tag == requested.Tag &&
		existing.Mode == requested.Mode &&
		existing.Topology == requested.Topology
}

// activeTopologyName reports the topology a stored settings value belongs
// to, defaulting a pre-ADR-0015 label's empty Topology to services - the
// only shape that ever existed before this ADR, so removing or destroying
// one must assume it, not error.
func activeTopologyName(settings TailscaleSettings) string {
	if settings.Topology == "" {
		return TailnetTopologyServices
	}
	return settings.Topology
}

// controlSeam is the only reader of the hidden headscale control seam's env
// vars: it returns the pre-minted auth key and control URL a topology's
// provider joins with, and whether the seam is configured at all (both vars
// set). Everything that behaves differently under the seam goes through here,
// so promoting the seam to first-class headscale support - or to another VPN
// provider's control plane - has one door to open (ADR-0010).
func controlSeam() (authKey, controlURL string, ok bool) {
	authKey, controlURL = os.Getenv(envAuthKey), os.Getenv(envControlURL)
	return authKey, controlURL, authKey != "" && controlURL != ""
}

// controlSeamActive reports whether the hidden headscale control seam is
// configured, replacing OAuth credential minting and the Tailscale SaaS API.
func controlSeamActive() bool {
	_, _, ok := controlSeam()
	return ok
}

// waitForFunnel polls the active topology's reader until it reports a Funnel
// serving the named app, and returns the endpoint's "host:port" - the public
// address the funnel serves on. Timeout and poll are parameters so tests can
// run the timeout path fast.
func waitForFunnel(ctx context.Context, reader TailnetReader, name, hint string, timeout, poll time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		status, err := reader.ServeStatus(ctx)
		if err == nil {
			if hostPort, ok := status.Funnels[name]; ok {
				return hostPort, nil
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("funnel did not activate for %s; check %s and that your tailnet ACL grants the funnel node attribute", name, hint)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(poll):
		}
	}
}

// funnelURL renders a funnel endpoint's "host:port" as the URL users open,
// with the default HTTPS port elided.
func funnelURL(hostPort string) string {
	if host, ok := strings.CutSuffix(hostPort, ":443"); ok {
		return "https://" + host
	}
	return "https://" + hostPort
}
