package docker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// lookupHost is the injectable resolver seam for the public DNS preflight.
var lookupHost = net.DefaultResolver.LookupHost

// PublicDNSWarning is the warn-only DNS check for public deploys. It never
// blocks the deploy: a failed lookup yields a warning that TLS issuance will
// fail until DNS points here, a successful one echoes the address for eyeball
// verification, and other exposures skip it entirely.
func PublicDNSWarning(ctx context.Context, exposure Exposure, host string) string {
	if exposure != ExposurePublic {
		return ""
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	addrs, err := lookupHost(ctx, host)
	if err == nil && len(addrs) == 0 {
		err = errors.New("no addresses returned")
	}
	if err != nil {
		return fmt.Sprintf("could not resolve %s (%v); deploying anyway, but TLS certificate issuance will fail until a DNS record points %s at this server", host, err, host)
	}
	return fmt.Sprintf("%s resolves to %s; verify this is this server's address", host, strings.Join(addrs, ", "))
}
