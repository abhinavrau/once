package command

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/basecamp/once/internal/docker"
)

// lookupHost is the injectable resolver seam for the public DNS preflight.
var lookupHost = net.DefaultResolver.LookupHost

// warnPublicDNS surfaces the warn-only DNS preflight for public deploys,
// mirroring warnShadowedServiceName's role for tailnet ones.
func warnPublicDNS(ctx context.Context, settings docker.ApplicationSettings) {
	if msg := dnsPreflight(ctx, settings.Exposure, settings.Host); msg != "" {
		fmt.Println(msg)
	}
}

// dnsPreflight is the warn-only DNS check for public deploys. It never blocks
// the deploy: a failed lookup yields a warning that TLS issuance will fail
// until DNS points here, a successful one echoes the address for eyeball
// verification, and other exposures skip it entirely.
func dnsPreflight(ctx context.Context, exposure docker.Exposure, host string) string {
	if exposure != docker.ExposurePublic {
		return ""
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	addrs, err := lookupHost(ctx, host)
	if err == nil && len(addrs) == 0 {
		err = errors.New("no addresses returned")
	}
	if err != nil {
		return fmt.Sprintf("Warning: could not resolve %s (%v). Deploying anyway, but TLS certificate issuance will fail until a DNS record points %s at this server.", host, err, host)
	}
	return fmt.Sprintf("%s resolves to %s. Verify this is this server's address.", host, strings.Join(addrs, ", "))
}
