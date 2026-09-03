package docker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// LookupHost is the injectable resolver seam for the DNS preflights. It is
// exported so tests in other packages can swap it as well, rather than
// duplicating the check or resolving for real.
var LookupHost = net.DefaultResolver.LookupHost

const (
	publicLookupTimeout = 5 * time.Second

	// tailnetLookupTimeout is shorter: it runs while the user waits on a status
	// line, not once per deploy.
	tailnetLookupTimeout = 2 * time.Second
)

// PublicDNSWarning is the warn-only DNS check for public deploys. It never
// blocks the deploy: a failed lookup yields a warning that TLS issuance will
// fail until DNS points here, a successful one echoes the address for eyeball
// verification, and other exposures skip it entirely.
func PublicDNSWarning(ctx context.Context, exposure Exposure, host string) string {
	if exposure != ExposurePublic {
		return ""
	}

	ctx, cancel := context.WithTimeout(ctx, publicLookupTimeout)
	defer cancel()

	addrs, err := LookupHost(ctx, host)
	if err == nil && len(addrs) == 0 {
		err = errors.New("no addresses returned")
	}
	if err != nil {
		return fmt.Sprintf("could not resolve %s (%v); deploying anyway, but TLS certificate issuance will fail until a DNS record points %s at this server", host, err, host)
	}
	return fmt.Sprintf("%s resolves to %s; verify this is this server's address", host, strings.Join(addrs, ", "))
}

// HostTailnetSuffix reads which tailnet this machine itself is on: the
// MagicDNS suffix of the host's own tailscaled, and whether one is running at
// all. It exists so the warning below can tell "wrong machine" apart from
// "broken MagicDNS" - the app's tailnet is Once's, resolved through its own
// topology, and never has to be the one the host is on. A running tailscaled
// whose suffix cannot be read reports on-tailnet with an empty suffix.
// Injectable like LookupHost so tests classify without a host daemon.
var HostTailnetSuffix = func(ctx context.Context) (suffix string, onTailnet bool) {
	if !hostTailscaledPresent() {
		return "", false
	}
	suffix, err := NewHostTailnetReader().MagicDNSSuffix(ctx)
	if err != nil {
		return "", true
	}
	return suffix, true
}

// TailnetDNSWarning is the warn-only check behind a tailnet service URL that
// once is about to show, so a user learns why the printed URL is dead here
// instead of concluding the deploy failed. It never blocks and never errors.
// The service's registration is already verified through the tailnet's own
// channels; this check only classifies why this machine cannot open the URL:
// not on any tailnet, on a different tailnet than Once (both simply name the
// right device to open it from), or on the same one - the only case where
// MagicDNS advice applies. Callers decide which URLs are worth resolving - a
// service that is known to be absent should be passed as an empty URL, which
// skips the lookup.
func TailnetDNSWarning(ctx context.Context, serviceURL string) string {
	if serviceURL == "" {
		return ""
	}

	host := hostnameOf(serviceURL)
	if host == "" {
		return ""
	}

	ctx, cancel := context.WithTimeout(ctx, tailnetLookupTimeout)
	defer cancel()

	if addrs, err := LookupHost(ctx, host); err == nil && len(addrs) > 0 {
		return ""
	}

	expected := suffixOf(host)
	hostSuffix, onTailnet := HostTailnetSuffix(ctx)
	switch {
	case expected == "":
	case !onTailnet:
		return fmt.Sprintf("%s will not resolve here: this machine is not on a tailnet. Open it from a device on %s.", host, expected)
	case hostSuffix != "" && !strings.EqualFold(hostSuffix, expected):
		return fmt.Sprintf("%s will not resolve here: this machine is on tailnet %s, not %s. Open it from a device on %s.", host, hostSuffix, expected, expected)
	}

	return fmt.Sprintf("%s does not resolve from this machine. The service may still be registering; or, if this machine is on the tailnet, its MagicDNS may be broken. Other tailnet devices may be unaffected.", host)
}

// Helpers

func hostnameOf(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

// suffixOf strips a tailnet host's first label, leaving the MagicDNS suffix
// the name belongs to.
func suffixOf(host string) string {
	if _, suffix, found := strings.Cut(host, "."); found {
		return suffix
	}
	return ""
}
