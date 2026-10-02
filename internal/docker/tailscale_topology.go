package docker

import (
	"context"
	"errors"
	"fmt"

	"github.com/containerd/errdefs"
)

// Tailnet Topology values (ADR-0015): the install-wide shape of how
// Applications reach the tailnet, named by shape and never by the software
// that implements it. Always written explicitly into the provider
// container's label - never inferred from the container name.
const (
	TailnetTopologyServices = "services"
	TailnetTopologyNodes    = "nodes"
)

// ValidateTailscaleTopology rejects a requested topology other than services,
// nodes, or empty (empty keeps an already-enabled install's stored topology,
// or defaults a fresh enable to nodes - see resolveTopology).
func ValidateTailscaleTopology(topology string) error {
	switch topology {
	case "", TailnetTopologyServices, TailnetTopologyNodes:
		return nil
	}
	return fmt.Errorf("invalid tailnet topology %q: must be %q or %q", topology, TailnetTopologyServices, TailnetTopologyNodes)
}

// ErrOlderOnceSetup is what every read path (session, LoadSettings) reports
// against a provider container whose label predates ADR-0015 and so carries
// no topology: no backward compatibility, the fix is re-running enable, which
// resolveTopology rebuilds as nodes unless --topology services is given.
// Exported so every frontend (CLI status/list, the dashboard, the enable
// form) can show this exact sentence once, instead of a stack of wrapped
// inspect errors (ticket 05).
var ErrOlderOnceSetup = errors.New("the Tailscale setup is from an older version of Once; re-run `once tailscale enable`")

// tailnetTopology is the private seam behind the Tailscale facade (ADR-0015):
// one implementation per Tailnet Topology, holding everything that varies by
// shape. Adapters are stateless with respect to a particular install - every
// method that needs the current settings takes them explicitly - so the
// facade can freely construct one to inspect, boot, or tear down either
// topology regardless of which is currently active.
//
// verify and status also take settings explicitly, deviating from this
// ticket's starting sketch (which had the facade's session bind them
// implicitly): both need settings.Tag and settings.ClientID/Secret for their
// cloud-side checks, which a settings-free signature can't carry. See ticket
// 03's Comments for the full account of what changed from the sketch.
type tailnetTopology interface {
	// boot idempotently brings up this topology's containers from settings:
	// ensures everything is running if it already matches, otherwise creates
	// it. Callers that need rebuild-on-change semantics (the facade's Enable)
	// call remove first.
	boot(ctx context.Context, settings TailscaleSettings) error
	// remove tears down this topology's containers, keeping any state volume
	// that carries node identity (a re-enable must not churn Magic DNS).
	// settings is the topology being removed - nodes uses it to best-effort
	// delete the per-app tailnet devices through the Runtime Credential
	// (ADR-0015); Services ignores it. Used on its own for a rebuild that
	// keeps the same topology.
	remove(ctx context.Context, settings TailscaleSettings) error
	// leave is remove for an install that stops using this topology - a
	// switch to the other one, or a disable - plus a best-effort delete of
	// whatever the topology created in the tailnet that holds a Magic DNS
	// name: Services' Tailscale Services. Never fails over that delete.
	leave(ctx context.Context, settings TailscaleSettings) error
	// destroy removes everything leave does, plus the state volume - full
	// cleanup, used by teardown.
	destroy(ctx context.Context, settings TailscaleSettings) error
	// removeApp best-effort releases whatever tailnet identity this topology
	// created for the app on host, once that app is removed. Never fails: an
	// app removal that is otherwise done must not be undone by the tailnet.
	removeApp(ctx context.Context, settings TailscaleSettings, host string)
	// relabel cheaply rewrites this topology's stored settings label (Docker
	// labels are immutable on a live container) without disturbing node
	// identity or other supporting state - the write-through path
	// DomainSuffix and WaitForFunnelActive use to bake in a value they just
	// learned.
	relabel(ctx context.Context, settings TailscaleSettings) error
	// appLabels returns the labels that expose an app under this topology,
	// under its Host, with the given Funnel public port (0 for none).
	appLabels(host, network string, funnelPort int) map[string]string
	// probe is the enable-time credential/preflight check. It returns
	// settings with any topology-owned defaulting resolved (e.g. Services'
	// Mode) baked in, and a one-line discoverability notice (or "") to
	// surface as a warning - e.g. Services' "--mode host" nudge.
	probe(ctx context.Context, settings TailscaleSettings) (TailscaleSettings, string, error)
	// reader returns the Tailnet Reader bound to settings.
	reader(settings TailscaleSettings) TailnetReader
	// verify confirms the expected apps are reachable after enable.
	verify(ctx context.Context, settings TailscaleSettings, expected []string, progress DeployProgressCallback) error
	// status reports each named app's current tailnet state.
	status(ctx context.Context, settings TailscaleSettings, names []string) ([]ServiceStatus, error)
	// funnelPort allocates self's Funnel public port among the namespace's
	// other apps, or (0, nil) for a topology that needs no allocation.
	funnelPort(self *Application, apps []*Application) (int, error)
	// validateHost rejects a name that cannot be exposed under this topology.
	// ctx and settings are for nodes' extra deploy-time check (a live devices-
	// list lookup against the Runtime Credential's tailnet, skipped on the
	// Control Seam); Services' shape-only check ignores both.
	validateHost(ctx context.Context, settings TailscaleSettings, name string) error
	// logsHint names the container whose logs explain a tailnet read that
	// came back short, for error messages.
	logsHint() string
	// facts returns this topology's static, context-free data for frontends:
	// sensible enable defaults, the OAuth scopes the Runtime Credential
	// needs, the tailnet policy snippet, the available --mode choices (nil
	// where mode does not apply), and the setup steps shown before enable.
	facts() TopologyFacts
}

// TopologyFacts is a topology's static data, independent of any particular
// install: sensible enable defaults, the OAuth scopes the Runtime Credential
// needs, the tailnet policy snippet, the available --mode choices (nil where
// mode does not apply), and the setup steps shown before enable. Every field
// is a plain value or a function of plain values - no adapter type leaks
// through - so frontends can render a topology's specifics without typing
// out any vendor's name, mode list, scope list, or ACL text themselves
// (ticket 05).
type TopologyFacts struct {
	DefaultSettings TailscaleSettings
	OAuthScopes     []string
	PolicySnippet   func(tag string) string
	ModeChoices     []string
	SetupIntro      string
}

// TailscaleTopologyFacts returns topology's static facts, for a frontend that
// has not yet enabled Tailscale (and so has no *Namespace to build a real
// adapter from) - the enable form's topology selector and the CLI's flag help
// both need this before any install exists. facts() never touches an
// adapter's namespace, so an unbound adapter is enough. topology must be
// TailnetTopologyServices or TailnetTopologyNodes.
func TailscaleTopologyFacts(topology string) (TopologyFacts, error) {
	switch topology {
	case TailnetTopologyServices:
		return (&servicesTopology{}).facts(), nil
	case TailnetTopologyNodes:
		return (&nodesTopology{}).facts(), nil
	default:
		return TopologyFacts{}, fmt.Errorf("invalid tailnet topology %q: must be %q or %q", topology, TailnetTopologyServices, TailnetTopologyNodes)
	}
}

// topologyFor returns the adapter for a resolved topology (never ""; callers
// pass resolveTopology's result).
func (t *Tailscale) topologyFor(topology string) (tailnetTopology, error) {
	switch topology {
	case TailnetTopologyServices:
		return newServicesTopology(t.namespace), nil
	case TailnetTopologyNodes:
		return newNodesTopology(t.namespace), nil
	default:
		return nil, fmt.Errorf("invalid tailnet topology %q", topology)
	}
}

// providerContainerNames names every container whose existence can mean
// Tailscale is enabled, in no particular order: exactly one exists per
// install, naming which Tailnet Provider - and so which topology - is
// active. once-docktail is the Services topology's provider; once-tsdproxy
// is the Nodes topology's.
func (t *Tailscale) providerContainerNames() []string {
	return []string{t.namespace.name + "-docktail", t.namespace.name + "-tsdproxy"}
}

// loadStoredSettings reads whichever provider container's label already
// exists, tolerating a pre-ADR-0015 label with no topology - unlike
// LoadSettings, whose callers must never operate on an ambiguous topology,
// Enable needs to recognize an old install so it can self-heal it, not refuse
// it. found is false when no provider container exists at all (a fresh
// install).
func (t *Tailscale) loadStoredSettings(ctx context.Context) (settings TailscaleSettings, found bool, err error) {
	for _, name := range t.providerContainerNames() {
		info, err := t.namespace.client.ContainerInspect(ctx, name)
		if errdefs.IsNotFound(err) {
			continue
		}
		if err != nil {
			return TailscaleSettings{}, false, fmt.Errorf("inspecting tailnet provider container %s: %w", name, err)
		}
		settings, err := UnmarshalTailscaleSettings(info.Config.Labels[labelKey])
		if err != nil {
			return TailscaleSettings{}, false, fmt.Errorf("unmarshalling tailnet provider settings: %w", err)
		}
		return settings, true, nil
	}
	return TailscaleSettings{}, false, nil
}

// resolveTopology is the only implementation of the Tailnet Topology
// resolution rule (ADR-0015): an explicit request wins; an empty one keeps an
// already-enabled install's stored topology; a fresh enable - and a
// pre-ADR-0015 label whose stored topology is empty - defaults to nodes.
// Needs no daemon: it only inspects the provider containers and unmarshals
// their label.
func (t *Tailscale) resolveTopology(ctx context.Context, requested string) (string, error) {
	if requested != "" {
		return requested, nil
	}
	existing, found, err := t.loadStoredSettings(ctx)
	if err != nil {
		return "", err
	}
	if found && existing.Topology != "" {
		return existing.Topology, nil
	}
	return TailnetTopologyNodes, nil
}

// topologyRoutesTailnetAppsViaProxy reports whether a topology's tailnet apps
// go through the Proxy (nodes: TSDProxy forwards to it, Host preserved) or
// straight to the app container (services: Docktail proxies to the container
// IP itself). application.go's registerRouting and admin.go's create are the
// two places that must register (or skip) a Proxy route for a tailnet app
// based on exactly this - factored out as its own decision so it is testable
// without a topology adapter or a namespace.
func topologyRoutesTailnetAppsViaProxy(topology string) bool {
	return topology == TailnetTopologyNodes
}

// validateModeTopologyCombination rejects a requested Mode against the
// resolved nodes topology: nodes has no tailscaled of its own, so Mode
// (host/sidecar) is a Services-only concept (ADR-0015) with nothing to
// choose under nodes. An empty Mode is always fine - it is what every
// nodes enable sends, since nothing ever resolves Mode for it.
func validateModeTopologyCombination(mode, topology string) error {
	if mode != "" && topology == TailnetTopologyNodes {
		return fmt.Errorf("--mode does not apply under the %s topology: it has no tailscaled of its own to choose a mode for", TailnetTopologyNodes)
	}
	return nil
}
