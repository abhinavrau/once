package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/client"
)

// TailscaleDaemon reads state from a local tailscaled via `tailscale ... --json`:
// the tailnet's Magic DNS suffix, which services are advertised, and funnel
// state. It is the single seam between Once and the daemon, whether that daemon
// is the Once-managed sidecar (docker exec) or a host tailscaled (host CLI).
// Every method is a read: ServeEnablementURL runs a serve command, but one the
// CLI is guaranteed to reject before it writes anything.
type TailscaleDaemon interface {
	// MagicDNSSuffix returns the tailnet's Magic DNS suffix (e.g.
	// tail1234.ts.net), or "" when the daemon doesn't report one yet.
	MagicDNSSuffix(ctx context.Context) (string, error)
	// MachineHostnames returns the Magic DNS labels of every machine on the
	// tailnet (this node and its peers), lowercased and sorted.
	MachineHostnames(ctx context.Context) ([]string, error)
	// SelfStatus returns this node's daemon version, tags, and node key, for
	// the host-mode pre-flight.
	SelfStatus(ctx context.Context) (TailscaleSelfStatus, error)
	// ServeStatus returns the daemon's serve config: advertised services and
	// funnel state.
	ServeStatus(ctx context.Context) (TailscaleServeStatus, error)
	// ServeEnablementURL returns the link a human must click to turn Serve on
	// for the tailnet, or "" when Serve is already enabled. An error means the
	// probe couldn't be run, which says nothing either way.
	ServeEnablementURL(ctx context.Context) (string, error)
}

// TailscaleSelfStatus is this node's slice of `tailscale status --json`.
type TailscaleSelfStatus struct {
	// BackendState is the daemon's ipn state (e.g. "Running", "NeedsLogin").
	BackendState string
	// Version is the daemon's version string (e.g. "1.88.4-t1234abcd").
	Version string
	// Tags holds the node's ACL tags (e.g. tag:once); empty when untagged.
	Tags []string
	// NodeKey is the node's public key ("nodekey:..."), matching the nodeKey
	// field of the cloud API's devices list.
	NodeKey string
}

// TailscaleServeStatus is the parsed `tailscale serve status --json` output.
type TailscaleServeStatus struct {
	// Services holds the bare names of advertised services (the svc: prefix
	// stripped), sorted.
	Services []string
	// AllowFunnel maps "host:port" to whether funnel is enabled there.
	AllowFunnel map[string]bool
	// FunnelBackends maps each proxy destination (e.g. http://172.18.0.5:80)
	// served through a funnel-enabled endpoint to that endpoint's "host:port"
	// (e.g. once-tailscale-1.tail1234.ts.net:443). This is what attributes a
	// funnel to a specific app and names its public URL: funnel is per-node,
	// so AllowFunnel alone can't say whose backend went public.
	FunnelBackends map[string]string
}

// The Serve-enablement probe: the `tailscale serve` invocation that reveals
// whether the tailnet has Serve enabled, and how long it is given to say so.
//
// The CLI checks the node's HTTPS capability before it touches the serve
// config or prefs, and when the tailnet lacks it, prints the enablement link
// and blocks until a human clicks it. The probe target is deliberately
// unservable: the CLI rejects its scheme just after that capability check, so a
// tailnet that already has Serve enabled exits immediately having written
// nothing. --https picks the HTTPS serve type the check is gated on, and --bg
// keeps the CLI from opening a foreground session on the way there.
const (
	serveProbeTarget  = "once-serve-probe://invalid"
	serveProbeTimeout = 5 * time.Second
)

var serveProbeArgs = []string{"serve", "--bg", "--https=443", serveProbeTarget}

// NewHostTailscaleDaemon returns a TailscaleDaemon backed by the host's
// tailscale CLI, for when Once uses a tailscaled already running on the host.
func NewHostTailscaleDaemon() TailscaleDaemon {
	return &cliTailscaleDaemon{run: runHostTailscale, probe: runHostTailscaleBounded}
}

// NewSidecarTailscaleDaemon returns a TailscaleDaemon that runs the tailscale
// CLI inside the named Once-managed sidecar container via docker exec.
func NewSidecarTailscaleDaemon(c *client.Client, containerName string) TailscaleDaemon {
	return &cliTailscaleDaemon{
		run: func(ctx context.Context, args ...string) (ExecResult, error) {
			return execInContainer(ctx, c, containerName, append([]string{"tailscale"}, args...))
		},
		probe: func(ctx context.Context, timeout time.Duration, args ...string) (ExecResult, error) {
			// The inner timeout(1) is what actually kills the CLI; the outer
			// context only stops Once waiting on the exec, since a docker exec's
			// process keeps running after its attach is dropped.
			ctx, cancel := context.WithTimeout(ctx, timeout+probeExecGrace)
			defer cancel()
			return execInContainer(ctx, c, containerName, boundedTailscaleCommand(timeout, args))
		},
	}
}

// cliTailscaleDaemon implements TailscaleDaemon over runners that execute
// `tailscale args...` somewhere (host CLI or docker exec) and report its
// stdout, stderr, and exit code. probe is the same, for a command that may
// never return on its own: it is killed at timeout and reports what the
// command printed before that.
type cliTailscaleDaemon struct {
	run   func(ctx context.Context, args ...string) (ExecResult, error)
	probe func(ctx context.Context, timeout time.Duration, args ...string) (ExecResult, error)
}

func (d *cliTailscaleDaemon) MagicDNSSuffix(ctx context.Context) (string, error) {
	statusJSON, err := d.statusJSON(ctx)
	if err != nil {
		return "", err
	}
	return parseMagicDNSSuffix(statusJSON)
}

func (d *cliTailscaleDaemon) MachineHostnames(ctx context.Context) ([]string, error) {
	statusJSON, err := d.statusJSON(ctx)
	if err != nil {
		return nil, err
	}
	return parseMachineHostnames(statusJSON)
}

func (d *cliTailscaleDaemon) SelfStatus(ctx context.Context) (TailscaleSelfStatus, error) {
	statusJSON, err := d.statusJSON(ctx)
	if err != nil {
		return TailscaleSelfStatus{}, err
	}
	return parseSelfStatus(statusJSON)
}

func (d *cliTailscaleDaemon) ServeStatus(ctx context.Context) (TailscaleServeStatus, error) {
	res, err := d.run(ctx, "serve", "status", "--json")
	if err != nil {
		return TailscaleServeStatus{}, err
	}
	if res.ExitCode != 0 {
		// An empty serve config is reported as a failure by some CLI versions;
		// it just means nothing is advertised yet.
		if isEmptyServeConfig(res.Stderr) {
			return TailscaleServeStatus{}, nil
		}
		return TailscaleServeStatus{}, fmt.Errorf("tailscale serve status: %s", strings.TrimSpace(res.Stderr))
	}
	return parseServeStatus(stripCLIWarnings(res.Stdout))
}

func (d *cliTailscaleDaemon) ServeEnablementURL(ctx context.Context) (string, error) {
	res, err := d.probe(ctx, serveProbeTimeout, serveProbeArgs...)
	if err != nil {
		return "", err
	}
	return parseServeEnablementURL(res.Stdout + res.Stderr), nil
}

// Private

// statusJSON runs `tailscale status --json` and returns its output with any
// leading CLI warnings stripped.
func (d *cliTailscaleDaemon) statusJSON(ctx context.Context) (string, error) {
	res, err := d.run(ctx, "status", "--json")
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("tailscale status: %s", strings.TrimSpace(res.Stderr))
	}
	return stripCLIWarnings(res.Stdout), nil
}

// FakeTailscaleDaemon implements TailscaleDaemon from canned CLI JSON, routing
// it through the same parsing as the real implementations. For tests.
type FakeTailscaleDaemon struct {
	StatusJSON  string // canned `tailscale status --json` output
	ServeJSON   string // canned `tailscale serve status --json` output
	ProbeOutput string // canned output of the Serve-enablement probe
	StatusErr   error
	ServeErr    error
	ProbeErr    error
}

func (f *FakeTailscaleDaemon) MagicDNSSuffix(ctx context.Context) (string, error) {
	if f.StatusErr != nil {
		return "", f.StatusErr
	}
	return parseMagicDNSSuffix(stripCLIWarnings(f.StatusJSON))
}

func (f *FakeTailscaleDaemon) MachineHostnames(ctx context.Context) ([]string, error) {
	if f.StatusErr != nil {
		return nil, f.StatusErr
	}
	return parseMachineHostnames(stripCLIWarnings(f.StatusJSON))
}

func (f *FakeTailscaleDaemon) SelfStatus(ctx context.Context) (TailscaleSelfStatus, error) {
	if f.StatusErr != nil {
		return TailscaleSelfStatus{}, f.StatusErr
	}
	return parseSelfStatus(stripCLIWarnings(f.StatusJSON))
}

func (f *FakeTailscaleDaemon) ServeStatus(ctx context.Context) (TailscaleServeStatus, error) {
	if f.ServeErr != nil {
		return TailscaleServeStatus{}, f.ServeErr
	}
	return parseServeStatus(stripCLIWarnings(f.ServeJSON))
}

func (f *FakeTailscaleDaemon) ServeEnablementURL(ctx context.Context) (string, error) {
	if f.ProbeErr != nil {
		return "", f.ProbeErr
	}
	return parseServeEnablementURL(f.ProbeOutput), nil
}

// Helpers

// hostCommandWaitDelay caps how long a killed host CLI's output pipes are
// still read after the kill. probeExecGrace is the slack the sidecar probe's
// exec allows beyond the timeout(1) that bounds the command itself.
const (
	hostCommandWaitDelay = time.Second
	probeExecGrace       = 5 * time.Second
)

// boundedTailscaleCommand wraps a tailscale invocation in timeout(1) - busybox
// provides it in the tailscale image - so a CLI that blocks forever is killed
// from inside the container, where a dropped docker exec attach cannot reach
// it. busybox's timeout counts whole seconds and reads 0 as "no timeout", so it
// is never handed less than one.
func boundedTailscaleCommand(timeout time.Duration, args []string) []string {
	seconds := max(int(timeout/time.Second), 1)
	return append([]string{"timeout", strconv.Itoa(seconds), "tailscale"}, args...)
}

// runHostTailscale executes the host's tailscale CLI, mapping a nonzero exit
// into ExecResult so the caller sees the same shape as a docker exec. A
// process that ran at all reports what it printed, including one the context
// killed part-way - that partial output is the whole point of the Serve probe.
// Only a command that never started is an error.
func runHostTailscale(ctx context.Context, args ...string) (ExecResult, error) {
	cmd := exec.CommandContext(ctx, "tailscale", args...)
	// Once the context has killed the command, stop waiting on its output pipes
	// too: anything the CLI left holding them open (a wrapper's orphaned child)
	// would otherwise keep the probe blocked for as long as that outlives it.
	cmd.WaitDelay = hostCommandWaitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil && cmd.ProcessState == nil {
		return ExecResult{}, fmt.Errorf("running tailscale CLI: %w", err)
	}
	return ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: cmd.ProcessState.ExitCode(),
	}, nil
}

// runHostTailscaleBounded runs the host's tailscale CLI under its own deadline,
// killing it at timeout and reporting what it printed before that.
func runHostTailscaleBounded(ctx context.Context, timeout time.Duration, args ...string) (ExecResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return runHostTailscale(ctx, args...)
}

// serveEnablementURL matches the link `tailscale serve` prints when the tailnet
// has never enabled Serve. The link comes from the control plane, so it is
// anchored on the Tailscale SaaS host - which is the only place Once probes,
// since the headscale control seam skips service verification entirely.
var serveEnablementURL = regexp.MustCompile(`https://login\.tailscale\.com/f/\S+`)

// parseServeEnablementURL picks the Serve enablement link out of the probe's
// output, or returns "" when there is none.
func parseServeEnablementURL(output string) string {
	return serveEnablementURL.FindString(output)
}

// stripCLIWarnings drops warning lines the tailscale CLI can print before the
// JSON (e.g. a client/daemon version mismatch).
func stripCLIWarnings(output string) string {
	if start := strings.Index(output, "{"); start > 0 {
		return output[start:]
	}
	return output
}

// isEmptyServeConfig reports whether a failed serve-status invocation just
// means no serve config exists yet. The phrasings are the serve/funnel-specific
// ones docktail treats as empty across tailscale CLI versions; deliberately not
// its generic "not found"/"does not exist" matches, which would swallow real
// failures like a missing tailscaled socket. An unrecognized phrasing surfaces
// as an error rather than a silently empty status.
func isEmptyServeConfig(stderr string) bool {
	lower := strings.ToLower(stderr)
	for _, phrase := range []string{"no serve config", "no services", "nothing to show", "no funnel"} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// parseMagicDNSSuffix reads the tailnet's Magic DNS suffix from `tailscale
// status --json` output, preferring CurrentTailnet.MagicDNSSuffix over its
// deprecated top-level alias.
func parseMagicDNSSuffix(statusJSON string) (string, error) {
	var status struct {
		CurrentTailnet struct {
			MagicDNSSuffix string
		}
		MagicDNSSuffix string
	}
	if err := json.Unmarshal([]byte(statusJSON), &status); err != nil {
		return "", fmt.Errorf("parsing tailscale status: %w", err)
	}
	suffix := status.CurrentTailnet.MagicDNSSuffix
	if suffix == "" {
		suffix = status.MagicDNSSuffix
	}
	return strings.Trim(suffix, "."), nil
}

// parseMachineHostnames reads the Magic DNS label of every machine (Self and
// each Peer) from `tailscale status --json` output. The DNSName's first label
// is the authoritative Magic DNS entry (it can differ from HostName after
// dedup renames); HostName is the fallback for nodes without one yet.
func parseMachineHostnames(statusJSON string) ([]string, error) {
	type node struct {
		HostName string
		DNSName  string
	}
	var status struct {
		Self node
		Peer map[string]node
	}
	if err := json.Unmarshal([]byte(statusJSON), &status); err != nil {
		return nil, fmt.Errorf("parsing tailscale status: %w", err)
	}

	var names []string
	add := func(n node) {
		name, _, _ := strings.Cut(n.DNSName, ".")
		if name == "" {
			name = n.HostName
		}
		if name != "" {
			names = append(names, strings.ToLower(name))
		}
	}
	add(status.Self)
	for _, peer := range status.Peer {
		add(peer)
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}

// parseSelfStatus reads this node's daemon version, tags, and node key from
// `tailscale status --json` output. Self.PublicKey is the node key, in the
// same "nodekey:..." form the cloud API's devices list reports.
func parseSelfStatus(statusJSON string) (TailscaleSelfStatus, error) {
	var status struct {
		BackendState string
		Version      string
		Self         struct {
			PublicKey string
			Tags      []string
		}
	}
	if err := json.Unmarshal([]byte(statusJSON), &status); err != nil {
		return TailscaleSelfStatus{}, fmt.Errorf("parsing tailscale status: %w", err)
	}
	return TailscaleSelfStatus{
		BackendState: status.BackendState,
		Version:      status.Version,
		Tags:         status.Self.Tags,
		NodeKey:      status.Self.PublicKey,
	}, nil
}

// parseServeStatus reads advertised services and funnel state from `tailscale
// serve status --json` output (the daemon's ServeConfig).
func parseServeStatus(serveJSON string) (TailscaleServeStatus, error) {
	var config struct {
		Services    map[string]json.RawMessage
		AllowFunnel map[string]bool
		Web         map[string]struct {
			Handlers map[string]struct {
				Proxy string
			}
		}
	}
	if err := json.Unmarshal([]byte(serveJSON), &config); err != nil {
		return TailscaleServeStatus{}, fmt.Errorf("parsing tailscale serve status: %w", err)
	}

	var services []string
	for name := range config.Services {
		services = append(services, strings.TrimPrefix(name, "svc:"))
	}
	slices.Sort(services)

	backends := map[string]string{}
	for hostPort, enabled := range config.AllowFunnel {
		if !enabled {
			continue
		}
		for _, handler := range config.Web[hostPort].Handlers {
			if handler.Proxy != "" {
				backends[handler.Proxy] = hostPort
			}
		}
	}

	return TailscaleServeStatus{Services: services, AllowFunnel: config.AllowFunnel, FunnelBackends: backends}, nil
}
