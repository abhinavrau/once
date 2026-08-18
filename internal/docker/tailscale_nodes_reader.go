package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// tsdproxyReaderTimeout bounds one request to the admin API, the same
// doctrine as tailscaleAPIRequestTimeout: a captive portal or a wedged
// TSDProxy must not hang a read forever.
const tsdproxyReaderTimeout = 10 * time.Second

// tsdproxyProxy is the subset of TSDProxy's GET /api/v1/proxies response
// (v2.3.4, docs/content/docs/operations/api.md) Once reads: a proxy's name
// (the app's Host), whether it has joined and is serving, its own tailnet
// URL, and whether any of its ports has Funnel on.
type tsdproxyProxy struct {
	Name   string         `json:"name"`
	Status string         `json:"status"`
	URL    string         `json:"url"`
	Ports  []tsdproxyPort `json:"ports"`
}

type tsdproxyPort struct {
	Funnel bool `json:"funnel"`
}

type tsdproxyProxiesResponse struct {
	Proxies []tsdproxyProxy `json:"proxies"`
}

// tsdproxyStatusRunning is the one Proxy Lifecycle state (advanced/tailscale.md)
// that means "joined the tailnet and serving": Initializing, Starting,
// Authenticating, Stopping, Stopped, and Error all read as not-yet-reachable.
const tsdproxyStatusRunning = "Running"

// parseTSDProxyProxies decodes a GET /api/v1/proxies response body. Both the
// production HTTP reader and the reader's tests (fed a canned fixture) go
// through this one parser, so a fixture drift between them is impossible.
func parseTSDProxyProxies(body []byte) ([]tsdproxyProxy, error) {
	var resp tsdproxyProxiesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decoding tsdproxy /api/v1/proxies response: %w", err)
	}
	return resp.Proxies, nil
}

// tsdproxyReader is the Nodes topology's TailnetReader: TSDProxy's REST API
// over the loopback port Once publishes it on (nodesTopology binds the admin
// API to 127.0.0.1 only). SelfStatus and ServeEnablementURL are Services-only
// concepts with no TSDProxy analogue - reached by nothing under nodes, since
// mode resolution and the Serve-enablement probe are Services-topology
// concerns (ADR-0015) - so they return zero values rather than an error.
type tsdproxyReader struct {
	baseURL string
	client  *http.Client
}

func newTSDProxyReader(baseURL string) *tsdproxyReader {
	return &tsdproxyReader{baseURL: baseURL, client: &http.Client{Timeout: tsdproxyReaderTimeout}}
}

// MagicDNSSuffix reads the tailnet's Magic DNS suffix from the first joined
// node's own tailnet URL (e.g. "https://once-admin.tail1234.ts.net" ->
// "tail1234.ts.net") - nodes has no shared node of its own to ask, unlike
// Services' sidecar.
func (r *tsdproxyReader) MagicDNSSuffix(ctx context.Context) (string, error) {
	proxies, err := r.proxies(ctx)
	if err != nil {
		return "", err
	}
	for _, p := range proxies {
		if p.Status != tsdproxyStatusRunning || p.URL == "" {
			continue
		}
		if suffix, ok := suffixFromTailnetURL(p.URL); ok {
			return suffix, nil
		}
	}
	return "", nil
}

// MachineHostnames always reports none: under nodes every device Once cares
// about is an app itself (validateHost's device-collision check guards that
// directly), so there is no separate "existing machine shadows a Service"
// hazard for ShadowWarnings to guard against - that hazard is specific to
// Services' shared-node/Service-name split.
func (r *tsdproxyReader) MachineHostnames(ctx context.Context) ([]string, error) {
	return nil, nil
}

// SelfStatus is unreached under nodes: it only serves Services' host-mode
// pre-flight (ADR-0007), which nodes has no equivalent of.
func (r *tsdproxyReader) SelfStatus(ctx context.Context) (TailscaleSelfStatus, error) {
	return TailscaleSelfStatus{}, nil
}

// ServeStatus reports every joined-and-running proxy by name (the app's
// Host), and the "host:port" a live Funnel serves on for any that have one -
// always the app's own tailnet URL's host and 443, since Funnel is per-app
// under nodes with no shared port to allocate.
func (r *tsdproxyReader) ServeStatus(ctx context.Context) (TailscaleServeStatus, error) {
	proxies, err := r.proxies(ctx)
	if err != nil {
		return TailscaleServeStatus{}, err
	}

	var services []string
	funnels := map[string]string{}
	for _, p := range proxies {
		if p.Status != tsdproxyStatusRunning || p.Name == "" {
			continue
		}
		services = append(services, p.Name)
		if hasFunnel(p.Ports) {
			if host, ok := strings.CutPrefix(p.URL, "https://"); ok && host != "" {
				funnels[p.Name] = host + ":443"
			}
		}
	}
	return TailscaleServeStatus{Services: services, Funnels: funnels}, nil
}

// ServeEnablementURL is unreached under nodes: TSDProxy needs no per-tailnet
// "Serve enablement" step the way `tailscale serve` does; Funnel readiness is
// governed entirely by the tailnet ACL's funnel nodeAttr.
func (r *tsdproxyReader) ServeEnablementURL(ctx context.Context) (string, error) {
	return "", nil
}

// Private

func (r *tsdproxyReader) proxies(ctx context.Context) ([]tsdproxyProxy, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/api/v1/proxies", nil)
	if err != nil {
		return nil, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("contacting tsdproxy's admin API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, tailscaleAPIMaxResponse))
	if err != nil {
		return nil, fmt.Errorf("reading tsdproxy's admin API response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tsdproxy's admin API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return parseTSDProxyProxies(body)
}

// Helpers

// hasFunnel reports whether any of a proxy's ports has Funnel enabled.
func hasFunnel(ports []tsdproxyPort) bool {
	for _, p := range ports {
		if p.Funnel {
			return true
		}
	}
	return false
}

// suffixFromTailnetURL splits a node's own "https://<name>.<suffix>" tailnet
// URL into its Magic DNS suffix.
func suffixFromTailnetURL(rawURL string) (string, bool) {
	host, ok := strings.CutPrefix(rawURL, "https://")
	if !ok {
		host, ok = strings.CutPrefix(rawURL, "http://")
	}
	if !ok {
		return "", false
	}
	host = strings.TrimSuffix(host, "/")
	_, suffix, ok := strings.Cut(host, ".")
	if !ok || suffix == "" {
		return "", false
	}
	return suffix, true
}
