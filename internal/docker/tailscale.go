package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
)

// tsdproxyImage is pinned to an exact version, the same doctrine as proxyImage.
// Bumped via Once releases through the self-update mechanism.
const tsdproxyImage = "almeidapaulopt/tsdproxy:2.3.4"

const tsdproxyConfigName = "tsdproxy.yaml"

// Funnel durations: a short default so apps don't stay public by accident, and
// a hard 24h cap (see PRD "Out of Scope").
const (
	DefaultFunnelDuration = 10 * time.Minute
	MaxFunnelDuration     = 24 * time.Hour
)

// funnelActivation bounds how long we wait for tsdproxy to report a Funnel
// active before surfacing a failure. ponytail: fixed poll, fine for an
// interactive enable; lengthen if first-time activation proves slower.
const (
	funnelActivationTimeout = 15 * time.Second
	funnelActivationPoll    = time.Second
)

// registrationVerify bounds the post-enable wait for the first tailnet node (at
// minimum once-admin) to register. A logged rejection fails fast; otherwise we
// stop waiting and let `once tailscale status` report progress — a healthy node
// usually registers in seconds, but we never block enable on a slow tailnet.
const (
	registrationVerifyTimeout = 25 * time.Second
	registrationVerifyPoll    = 2 * time.Second
)

// tsdproxyRegistrationFailures are substrings tsdproxy/tsnet logs when the
// control plane rejects a node's registration — the tag-ownership case being the
// one this exists to catch. ponytail: substring match on tsdproxy's own words;
// extend the list if new rejection phrasings show up.
var tsdproxyRegistrationFailures = []string{
	"tags are invalid or not permitted",
	"is invalid or not permitted",
	"invalid authkey",
	"authkey is invalid",
}

var (
	ErrFunnelDurationInvalid = errors.New("funnel duration must be positive")
	ErrFunnelDurationTooLong = fmt.Errorf("funnel duration must not exceed %s", MaxFunnelDuration)
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

// The tsdproxy HTTP API serves /api/v1/proxies on port 8080 inside the
// container. We publish it bound to loopback only — the single deliberate
// exception to the zero-host-TCP-ports constraint, unreachable from any network
// interface — because the host cannot resolve container DNS names (and macOS
// Docker Desktop cannot reach bridge IPs).
const (
	tsdproxyAPIPort     = "8080"
	tsdproxyAPIHostBind = "127.0.0.1"
	tsdproxyAPIHostPort = "8484"
	tsdproxyAPIBaseURL  = "http://127.0.0.1:8484"
)

// Hidden control-server seam: when these env vars are set, the tsdproxy is
// configured with controlUrl + authKey instead of OAuth. No CLI/TUI exposes
// them — they exist to point once-tsdproxy at the headscale test harness, and
// are promotable to first-class headscale support later.
const (
	envControlURL = "ONCE_TAILSCALE_CONTROL_URL"
	envAuthKey    = "ONCE_TAILSCALE_AUTH_KEY"
)

type TailscaleSettings struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	// Tag is the Tailscale tag (e.g. tag:once) tsdproxy stamps on the auth keys it
	// mints via OAuth. A tagged OAuth client can only mint keys for tags it owns,
	// so this must be a tag the client owns in the tailnet ACL. Empty on the
	// headscale control seam, which uses a pre-minted auth key instead.
	Tag string `json:"tag,omitempty"`
	// APIKey authenticates host lookups to the tsdproxy API. Generated at enable
	// time and configured into tsdproxy; the host client sends it as a Bearer
	// token (connections via the published port are not seen as localhost).
	APIKey string `json:"apiKey,omitempty"`
	// DomainSuffix is the tailnet's MagicDNS base (e.g. tailnet-name.ts.net),
	// learned from the first tailnet URL tsdproxy reports. With it stored, app
	// tailnet URLs can be built without waiting on a per-app lookup.
	DomainSuffix string `json:"domainSuffix,omitempty"`
}

// TailnetURL builds an app's https://<app>.<suffix> tailnet URL from the stored
// domain suffix, or "" when the suffix isn't known yet.
func (s TailscaleSettings) TailnetURL(appName string) string {
	if s.DomainSuffix == "" {
		return ""
	}
	return "https://" + appName + "." + s.DomainSuffix
}

func UnmarshalTailscaleSettings(s string) (TailscaleSettings, error) {
	var settings TailscaleSettings
	err := json.Unmarshal([]byte(s), &settings)
	return settings, err
}

func (s TailscaleSettings) Marshal() string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Private

// normalizeTag prepends "tag:" to each comma-separated segment that lacks it, so
// a bare name like "once" becomes "tag:once" before it reaches the Tailscale API
// or the tsdproxy config. Already-prefixed segments pass through unchanged. A tag
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

// Tailscale manages the once-tsdproxy helper container, mirroring Proxy. The
// container's existence is the source of truth for "Tailscale enabled"; its
// settings live in the once label on the container itself.
type Tailscale struct {
	namespace    *Namespace
	Settings     *TailscaleSettings
	domainSuffix string // in-memory cache so a learned suffix survives a failed persist
}

func NewTailscale(ns *Namespace) *Tailscale {
	return &Tailscale{namespace: ns}
}

// Enable boots once-tsdproxy from the pinned image, mounting the Docker socket
// and the once-tsdproxy-data volume in userspace networking mode. Re-running
// with new credentials recreates only this container; the data volume (node
// identities, Magic DNS names) is left untouched.
func (t *Tailscale) Enable(ctx context.Context, settings TailscaleSettings) error {
	info, err := t.namespace.client.ContainerInspect(ctx, t.containerName())
	if err == nil {
		existing, _ := UnmarshalTailscaleSettings(info.Config.Labels[labelKey])
		if existing.ClientID == settings.ClientID && existing.ClientSecret == settings.ClientSecret && existing.Tag == settings.Tag {
			return t.ensureRunning(ctx, info)
		}
		// Credentials changed: recreate the container, keep the data volume. Keep
		// the existing API key so host lookups (CLI/TUI) keep working.
		settings.APIKey = existing.APIKey
		if err := t.removeContainer(ctx); err != nil {
			return err
		}
	} else if !errdefs.IsNotFound(err) {
		return fmt.Errorf("inspecting tsdproxy container: %w", err)
	}

	if settings.APIKey == "" {
		if settings.APIKey, err = randomID(32); err != nil {
			return fmt.Errorf("generating tsdproxy API key: %w", err)
		}
	}

	if err := t.pullImage(ctx); err != nil {
		return err
	}

	if err := t.create(ctx, settings); err != nil {
		return err
	}

	t.Settings = &settings

	// Retrofit running apps so they appear on the tailnet immediately, not just
	// on their next deploy. once-tsdproxy now exists, so the roll injects labels.
	return t.namespace.RollApplications(ctx)
}

// Disable stops and removes once-tsdproxy but keeps the once-tsdproxy-data
// volume, so node identities survive a later re-enable without suffix churn.
func (t *Tailscale) Disable(ctx context.Context) error {
	if err := t.removeContainer(ctx); err != nil {
		return err
	}
	t.Settings = nil

	// Roll running apps to strip the tsdproxy.* labels — once-tsdproxy is gone,
	// so Enabled() is now false and the roll recreates them without the labels.
	return t.namespace.RollApplications(ctx)
}

// Enabled is implicit in the existence of the once-tsdproxy container (the same
// doctrine the proxy follows).
func (t *Tailscale) Enabled(ctx context.Context) (bool, error) {
	_, err := t.namespace.client.ContainerInspect(ctx, t.containerName())
	if err == nil {
		return true, nil
	}
	if errdefs.IsNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("inspecting tsdproxy container: %w", err)
}

// LoadSettings reads the Tailscale settings stored in the once-tsdproxy
// container label, for pre-populating UIs. Errors if Tailscale isn't enabled.
func (t *Tailscale) LoadSettings(ctx context.Context) (TailscaleSettings, error) {
	info, err := t.namespace.client.ContainerInspect(ctx, t.containerName())
	if err != nil {
		return TailscaleSettings{}, fmt.Errorf("inspecting tsdproxy container: %w", err)
	}
	return UnmarshalTailscaleSettings(info.Config.Labels[labelKey])
}

// TailnetProxy is one entry from tsdproxy's lookup API: an app (or helper)
// exposed on the tailnet under its Magic DNS name.
type TailnetProxy struct {
	Name   string // matches the app's Settings.Name (the tsdproxy.name label)
	URL    string // tailnet URL, e.g. https://writebook.tailnet-name.ts.net
	Status string // tsdproxy proxy status, e.g. "Running"
	Funnel bool   // true when any port has Funnel enabled
}

// Proxies queries the once-tsdproxy lookup API over the loopback-published port
// and returns the registered tailnet proxies. Requires Tailscale enabled.
func (t *Tailscale) Proxies(ctx context.Context) ([]TailnetProxy, error) {
	info, err := t.namespace.client.ContainerInspect(ctx, t.containerName())
	if err != nil {
		return nil, fmt.Errorf("inspecting tsdproxy container: %w", err)
	}
	settings, err := UnmarshalTailscaleSettings(info.Config.Labels[labelKey])
	if err != nil {
		return nil, fmt.Errorf("reading tsdproxy settings: %w", err)
	}
	return fetchProxies(ctx, tsdproxyAPIBaseURL, settings.APIKey)
}

// ProxyByName returns the tailnet proxy tsdproxy reports for the named app, with
// found=false when it isn't (yet) listed. Used to surface the real Funnel state
// rather than assuming activation succeeded.
func (t *Tailscale) ProxyByName(ctx context.Context, name string) (TailnetProxy, bool, error) {
	proxies, err := t.Proxies(ctx)
	if err != nil {
		return TailnetProxy{}, false, err
	}
	for _, p := range proxies {
		if p.Name == name {
			return p, true, nil
		}
	}
	return TailnetProxy{}, false, nil
}

// VerifyRegistration waits briefly after enable for the first tailnet node to
// register. The enable-time mint probe proves a key CAN be minted, but the
// control plane enforces tag ownership (and credential validity) only at
// registration — so this is where a tag missing from tagOwners, or a revoked
// credential, actually surfaces. once-admin is always retrofitted on enable, so
// at least one node attempts to register even with no user apps deployed.
//
// Only rejections logged after enable started count: re-running enable after
// fixing the ACL keeps the same container (credentials unchanged), so its old
// rejection lines must not fail the retry. Returns nil (inconclusive — proceed)
// when nothing registers and nothing fresh is rejected before the timeout, so a
// merely slow tailnet never fails enable.
func (t *Tailscale) VerifyRegistration(ctx context.Context) error {
	since := time.Now()
	deadline := since.Add(registrationVerifyTimeout)
	for {
		proxies, err := t.Proxies(ctx)
		if err == nil {
			for _, p := range proxies {
				if p.URL != "" {
					return nil
				}
			}
		}
		if msg, _ := t.RegistrationError(ctx, since); msg != "" {
			return fmt.Errorf("Tailscale node registration was rejected by the control plane: %s; the configured tag must be listed in your tailnet ACL tagOwners (Access Controls → tagOwners) — minting an auth key for a tag does not grant the right to register a node with it; fix the ACL (or the credential) and re-run `once tailscale enable`", msg)
		}
		if time.Now().After(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(registrationVerifyPoll):
		}
	}
}

// RegistrationError returns the most recent once-tsdproxy log line reporting a
// node-registration rejection, or "" if none. It is the diagnostic behind an
// empty node list, since the control plane rejects a bad tag or credential only
// at registration. since bounds how far back to read (zero = the whole tail).
func (t *Tailscale) RegistrationError(ctx context.Context, since time.Time) (string, error) {
	opts := container.LogsOptions{ShowStdout: true, ShowStderr: true, Tail: "500"}
	if !since.IsZero() {
		opts.Since = since.Format(time.RFC3339)
	}
	reader, err := t.namespace.client.ContainerLogs(ctx, t.containerName(), opts)
	if err != nil {
		return "", fmt.Errorf("reading tsdproxy logs: %w", err)
	}
	defer reader.Close()

	var out bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &out, reader); err != nil {
		return "", fmt.Errorf("reading tsdproxy logs: %w", err)
	}
	return scanRegistrationError(out.String()), nil
}

// DomainSuffix returns the tailnet's MagicDNS suffix (e.g. tailnet-name.ts.net).
// It prefers the value persisted in settings; otherwise it derives the suffix
// from the first tailnet URL tsdproxy reports and persists it so later lookups
// (and apps whose node isn't up) can build URLs without a live proxy. Returns ""
// when no node has registered yet, so the suffix can't be known.
func (t *Tailscale) DomainSuffix(ctx context.Context) (string, error) {
	if t.domainSuffix != "" {
		return t.domainSuffix, nil
	}

	settings, err := t.LoadSettings(ctx)
	if err != nil {
		return "", err
	}
	if settings.DomainSuffix != "" {
		t.domainSuffix = settings.DomainSuffix
		return settings.DomainSuffix, nil
	}

	proxies, err := t.Proxies(ctx)
	if err != nil {
		return "", err
	}
	for _, p := range proxies {
		suffix := domainSuffixFromURL(p.URL)
		if suffix == "" {
			continue
		}
		// ponytail: Docker labels are immutable, so baking the suffix into the
		// settings label means one container recreate; ephemeral proxies
		// re-register on restart, so the churn is a one-time blip. Cache the suffix
		// first so a failed persist still short-circuits here next time rather than
		// recreating on every poll.
		t.domainSuffix = suffix
		settings.DomainSuffix = suffix
		_ = t.persistSettings(ctx, settings)
		return suffix, nil
	}
	return "", nil
}

// WaitForFunnelActive polls until tsdproxy reports the named app's Funnel
// active, or returns an error on timeout. Funnel needs the tailnet ACL's funnel
// node attribute, which Once can't manage — surfacing this prevents reporting a
// Funnel active when activation actually failed.
func (t *Tailscale) WaitForFunnelActive(ctx context.Context, name string) error {
	deadline := time.Now().Add(funnelActivationTimeout)
	var last TailnetProxy
	var found bool
	for {
		p, ok, err := t.ProxyByName(ctx, name)
		if err == nil && ok {
			last, found = p, true
			if p.Funnel {
				return nil
			}
		}
		if time.Now().After(deadline) {
			if found {
				return fmt.Errorf("funnel did not activate (proxy status %q); check that your tailnet ACL grants the funnel node attribute", last.Status)
			}
			return fmt.Errorf("funnel did not activate; check that the once-tsdproxy container is running and your tailnet ACL grants the funnel node attribute")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(funnelActivationPoll):
		}
	}
}

// Destroy removes both the container and the data volume (full cleanup, used by
// teardown — unlike Disable, which retains the volume).
func (t *Tailscale) Destroy(ctx context.Context) error {
	if err := t.removeContainer(ctx); err != nil {
		return err
	}
	if err := t.namespace.client.VolumeRemove(ctx, t.dataVolumeName(), true); err != nil {
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("removing tsdproxy volume: %w", err)
		}
	}
	t.Settings = nil
	return nil
}

// Private

func (t *Tailscale) containerName() string {
	return t.namespace.name + "-tsdproxy"
}

func (t *Tailscale) dataVolumeName() string {
	return t.namespace.name + "-tsdproxy-data"
}

// persistSettings rewrites the once-tsdproxy container's settings label by
// recreating it (Docker labels are immutable on a live container), keeping the
// data volume so node identities survive. Used to bake in a newly-learned domain
// suffix.
func (t *Tailscale) persistSettings(ctx context.Context, settings TailscaleSettings) error {
	if err := t.removeContainer(ctx); err != nil {
		return err
	}
	if err := t.create(ctx, settings); err != nil {
		return err
	}
	t.Settings = &settings
	return nil
}

func (t *Tailscale) ensureRunning(ctx context.Context, info container.InspectResponse) error {
	if !info.State.Running {
		if err := t.namespace.client.ContainerStart(ctx, info.ID, container.StartOptions{}); err != nil {
			return fmt.Errorf("starting tsdproxy container: %w", err)
		}
	}
	if label := info.Config.Labels[labelKey]; label != "" {
		settings, err := UnmarshalTailscaleSettings(label)
		if err != nil {
			return fmt.Errorf("unmarshalling tsdproxy settings: %w", err)
		}
		t.Settings = &settings
	}
	return nil
}

func (t *Tailscale) create(ctx context.Context, settings TailscaleSettings) error {
	name := t.containerName()

	resp, err := t.namespace.client.ContainerCreate(ctx,
		&container.Config{
			Image: tsdproxyImage,
			Labels: map[string]string{
				labelKey: settings.Marshal(),
			},
			ExposedPorts: nat.PortSet{nat.Port(tsdproxyAPIPort + "/tcp"): struct{}{}},
		},
		&container.HostConfig{
			// ponytail: no /dev/net/tun, no NET_ADMIN — tsnet runs userspace natively.
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyAlways},
			LogConfig:     ContainerLogConfig(),
			Binds:         []string{"/var/run/docker.sock:/var/run/docker.sock"},
			Mounts: []mount.Mount{
				{Type: mount.TypeVolume, Source: t.dataVolumeName(), Target: "/data"},
			},
			PortBindings: nat.PortMap{
				nat.Port(tsdproxyAPIPort + "/tcp"): []nat.PortBinding{
					{HostIP: tsdproxyAPIHostBind, HostPort: tsdproxyAPIHostPort},
				},
			},
		},
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				t.namespace.name: {},
			},
		},
		nil,
		name,
	)
	if err != nil {
		return fmt.Errorf("creating tsdproxy container: %w", err)
	}

	config := buildTSDProxyConfig(settings, os.Getenv(envControlURL), os.Getenv(envAuthKey))
	if err := t.copyConfig(ctx, resp.ID, config); err != nil {
		t.namespace.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return err
	}

	if err := t.namespace.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		t.namespace.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return fmt.Errorf("starting tsdproxy container: %w", err)
	}

	return nil
}

// copyConfig writes the tsdproxy config into /config before the container
// starts, mirroring how the proxy persists state via CopyToContainer. The tar
// carries the /config directory entry too, so it need not pre-exist.
func (t *Tailscale) copyConfig(ctx context.Context, containerID, config string) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	dir := "config/"
	if err := tw.WriteHeader(&tar.Header{Name: dir, Mode: 0o755, Typeflag: tar.TypeDir}); err != nil {
		return fmt.Errorf("writing config dir header: %w", err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: dir + tsdproxyConfigName, Mode: 0o644, Size: int64(len(config))}); err != nil {
		return fmt.Errorf("writing config header: %w", err)
	}
	if _, err := tw.Write([]byte(config)); err != nil {
		return fmt.Errorf("writing config data: %w", err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("closing config tar: %w", err)
	}

	if err := t.namespace.client.CopyToContainer(ctx, containerID, "/", &buf, container.CopyToContainerOptions{}); err != nil {
		return fmt.Errorf("copying tsdproxy config: %w", err)
	}
	return nil
}

func (t *Tailscale) pullImage(ctx context.Context) error {
	reader, err := t.namespace.client.ImagePull(ctx, tsdproxyImage, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pulling tsdproxy image: %w", err)
	}
	defer reader.Close()
	_, _ = io.Copy(io.Discard, reader)
	return nil
}

func (t *Tailscale) removeContainer(ctx context.Context) error {
	if err := t.namespace.client.ContainerRemove(ctx, t.containerName(), container.RemoveOptions{Force: true}); err != nil {
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("removing tsdproxy: %w", err)
		}
	}
	return nil
}

// Helpers

// tsdproxyLabels returns the labels that expose an app on the tailnet under its
// own Magic DNS name. Nil when Tailscale is disabled, so app containers carry
// the labels only while once-tsdproxy is running. The 80/http upstream matches
// Once's port-80 assumption; ephemeral nodes self-clean when the app is deleted.
//
// When funnel is set, the port label gains the tailscale_funnel option and the
// public side becomes 443/https (Funnel only serves HTTPS publicly).
func tsdproxyLabels(appName string, enabled, funnel bool) map[string]string {
	if !enabled {
		return nil
	}
	port := "80/http:80/http"
	if funnel {
		port = "443/https:80/http, tailscale_funnel"
	}
	return map[string]string{
		"tsdproxy.enable":    "true",
		"tsdproxy.name":      appName,
		"tsdproxy.port.1":    port,
		"tsdproxy.ephemeral": "true",
	}
}

// scanRegistrationError returns the most recent tsdproxy log line matching a
// known registration-rejection signature, or "" when none match.
func scanRegistrationError(logs string) string {
	var last string
	for _, line := range strings.Split(logs, "\n") {
		lower := strings.ToLower(line)
		for _, sig := range tsdproxyRegistrationFailures {
			if strings.Contains(lower, sig) {
				last = strings.TrimSpace(line)
				break
			}
		}
	}
	return last
}

// domainSuffixFromURL derives the tailnet MagicDNS suffix from a node URL by
// stripping the leading host label: https://writebook.foo.ts.net -> foo.ts.net.
func domainSuffixFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	_, suffix, ok := strings.Cut(u.Hostname(), ".")
	if !ok {
		return ""
	}
	return suffix
}

// fetchProxies queries the tsdproxy lookup API and returns the visible proxies.
// baseURL is parameterised so tests can point it at an httptest server.
func fetchProxies(ctx context.Context, baseURL, apiKey string) ([]TailnetProxy, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/proxies", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("querying tsdproxy API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tsdproxy API returned %s", resp.Status)
	}

	var body struct {
		Proxies []struct {
			Name   string `json:"name"`
			URL    string `json:"url"`
			Status string `json:"status"`
			Ports  []struct {
				Funnel bool `json:"funnel"`
			} `json:"ports"`
		} `json:"proxies"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding tsdproxy API response: %w", err)
	}

	proxies := make([]TailnetProxy, 0, len(body.Proxies))
	for _, p := range body.Proxies {
		var funnel bool
		for _, port := range p.Ports {
			funnel = funnel || port.Funnel
		}
		proxies = append(proxies, TailnetProxy{Name: p.Name, URL: p.URL, Status: p.Status, Funnel: funnel})
	}
	return proxies, nil
}

// buildTSDProxyConfig renders the tsdproxy YAML config. When controlURL and
// authKey are set (the hidden control seam), they replace OAuth entirely —
// OAuth is Tailscale-SaaS-only and cannot reach a headscale control plane.
func buildTSDProxyConfig(settings TailscaleSettings, controlURL, authKey string) string {
	var provider string
	if controlURL != "" && authKey != "" {
		provider = fmt.Sprintf("      authKey: \"%s\"\n      authKeyFile: \"\"\n      controlUrl: %s\n", authKey, controlURL)
	} else {
		provider = fmt.Sprintf("      clientId: \"%s\"\n      clientSecret: \"%s\"\n", settings.ClientID, settings.ClientSecret)
		if settings.Tag != "" {
			provider += fmt.Sprintf("      tags: \"%s\"\n", settings.Tag)
		}
	}
	return fmt.Sprintf(tsdproxyConfigTmpl, settings.APIKey, provider)
}

// tsdproxyConfigTmpl matches the config proven against tsdproxy:2 in the
// integration harness. %[1]s is the API key (Bearer auth for the lookup API),
// %[2]s is the provider auth block (indented 6 spaces).
const tsdproxyConfigTmpl = `defaultProxyProvider: default
apiKey: "%[1]s"
docker:
  local:
    host: unix:///var/run/docker.sock
    targetHostname: host.docker.internal
    tryDockerInternalNetwork: true
    defaultProxyProvider: default
tailscale:
  providers:
    default:
%[2]s  dataDir: /data/
http:
  hostname: 0.0.0.0
  port: 8080
log:
  level: info
  json: false
`
