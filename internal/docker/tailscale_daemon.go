package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/docker/docker/client"
)

// TailscaleDaemon reads state from a local tailscaled via `tailscale ... --json`:
// the tailnet's Magic DNS suffix, which services are advertised, and funnel
// state. It is the single seam between Once and the daemon, whether that daemon
// is the Once-managed sidecar (docker exec) or a host tailscaled (host CLI).
type TailscaleDaemon interface {
	// MagicDNSSuffix returns the tailnet's Magic DNS suffix (e.g.
	// tail1234.ts.net), or "" when the daemon doesn't report one yet.
	MagicDNSSuffix(ctx context.Context) (string, error)
	// MachineHostnames returns the Magic DNS labels of every machine on the
	// tailnet (this node and its peers), lowercased and sorted.
	MachineHostnames(ctx context.Context) ([]string, error)
	// ServeStatus returns the daemon's serve config: advertised services and
	// funnel state.
	ServeStatus(ctx context.Context) (TailscaleServeStatus, error)
}

// TailscaleServeStatus is the parsed `tailscale serve status --json` output.
type TailscaleServeStatus struct {
	// Services holds the bare names of advertised services (the svc: prefix
	// stripped), sorted.
	Services []string
	// AllowFunnel maps "host:port" to whether funnel is enabled there.
	AllowFunnel map[string]bool
}

// NewHostTailscaleDaemon returns a TailscaleDaemon backed by the host's
// tailscale CLI, for when Once uses a tailscaled already running on the host.
func NewHostTailscaleDaemon() TailscaleDaemon {
	return &cliTailscaleDaemon{run: runHostTailscale}
}

// NewSidecarTailscaleDaemon returns a TailscaleDaemon that runs the tailscale
// CLI inside the named Once-managed sidecar container via docker exec.
func NewSidecarTailscaleDaemon(c *client.Client, containerName string) TailscaleDaemon {
	return &cliTailscaleDaemon{run: func(ctx context.Context, args ...string) (ExecResult, error) {
		return execInContainer(ctx, c, containerName, append([]string{"tailscale"}, args...))
	}}
}

// cliTailscaleDaemon implements TailscaleDaemon over a runner that executes
// `tailscale args...` somewhere (host CLI or docker exec) and reports its
// stdout, stderr, and exit code.
type cliTailscaleDaemon struct {
	run func(ctx context.Context, args ...string) (ExecResult, error)
}

func (d *cliTailscaleDaemon) MagicDNSSuffix(ctx context.Context) (string, error) {
	res, err := d.run(ctx, "status", "--json")
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("tailscale status: %s", strings.TrimSpace(res.Stderr))
	}
	return parseMagicDNSSuffix(stripCLIWarnings(res.Stdout))
}

func (d *cliTailscaleDaemon) MachineHostnames(ctx context.Context) ([]string, error) {
	res, err := d.run(ctx, "status", "--json")
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("tailscale status: %s", strings.TrimSpace(res.Stderr))
	}
	return parseMachineHostnames(stripCLIWarnings(res.Stdout))
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

// FakeTailscaleDaemon implements TailscaleDaemon from canned CLI JSON, routing
// it through the same parsing as the real implementations. For tests.
type FakeTailscaleDaemon struct {
	StatusJSON string // canned `tailscale status --json` output
	ServeJSON  string // canned `tailscale serve status --json` output
	StatusErr  error
	ServeErr   error
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

func (f *FakeTailscaleDaemon) ServeStatus(ctx context.Context) (TailscaleServeStatus, error) {
	if f.ServeErr != nil {
		return TailscaleServeStatus{}, f.ServeErr
	}
	return parseServeStatus(stripCLIWarnings(f.ServeJSON))
}

// Helpers

// runHostTailscale executes the host's tailscale CLI, mapping a nonzero exit
// into ExecResult so the caller sees the same shape as a docker exec.
func runHostTailscale(ctx context.Context, args ...string) (ExecResult, error) {
	cmd := exec.CommandContext(ctx, "tailscale", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return ExecResult{}, fmt.Errorf("running tailscale CLI: %w", err)
	}
	return ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: cmd.ProcessState.ExitCode(),
	}, nil
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

// parseServeStatus reads advertised services and funnel state from `tailscale
// serve status --json` output (the daemon's ServeConfig).
func parseServeStatus(serveJSON string) (TailscaleServeStatus, error) {
	var config struct {
		Services    map[string]json.RawMessage
		AllowFunnel map[string]bool
	}
	if err := json.Unmarshal([]byte(serveJSON), &config); err != nil {
		return TailscaleServeStatus{}, fmt.Errorf("parsing tailscale serve status: %w", err)
	}

	var services []string
	for name := range config.Services {
		services = append(services, strings.TrimPrefix(name, "svc:"))
	}
	slices.Sort(services)

	return TailscaleServeStatus{Services: services, AllowFunnel: config.AllowFunnel}, nil
}
