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

// TailnetDNSWarning is the warn-only check behind a tailnet service URL that
// once is about to show, so a user whose local resolver is wedged learns why
// the printed URL is dead instead of concluding the deploy failed. It never
// blocks and never errors. Every cause the warning names stays conditional:
// this machine is not required to be on the tailnet at all. Callers decide
// which URLs are worth resolving - a service that is known to be absent should
// be passed as an empty URL, which skips the lookup.
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

	return fmt.Sprintf("%s does not resolve from this machine. The service may still be registering; or, if this machine is on the tailnet, its MagicDNS may be broken - try `tailscale set --accept-dns=false` then `tailscale set --accept-dns=true`. Other tailnet devices may be unaffected.", host)
}

// Helpers

func hostnameOf(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}
