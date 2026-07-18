package docker

import (
	"errors"
	"fmt"
	"strings"
)

// Exposure states where an application is reachable. Exactly one per app,
// chosen explicitly and stored in the once label - never derived at read time
// (hostname shape is only used to backfill apps deployed before this field
// existed).
type Exposure string

const (
	ExposureLocal  Exposure = "local"
	ExposurePublic Exposure = "public"
)

var (
	ErrPublicHostRequired = errors.New("public exposure requires a fully-qualified hostname (e.g. app.example.com)")
	ErrHostNotLocal       = errors.New("local exposure requires a bare name or a .localhost hostname (use --expose public for a public hostname)")
)

func ParseExposure(s string) (Exposure, error) {
	switch Exposure(s) {
	case ExposureLocal, ExposurePublic:
		return Exposure(s), nil
	}
	return "", fmt.Errorf("invalid exposure %q: must be %q or %q", s, ExposureLocal, ExposurePublic)
}

// ExposureForHost derives an exposure from a hostname's shape, for backfilling
// apps that predate the exposure field: *.localhost (or no host) is local,
// real DNS is public.
func ExposureForHost(host string) Exposure {
	if host == "" || IsLocalhost(host) {
		return ExposureLocal
	}
	return ExposurePublic
}

// ResolveHost applies an exposure's naming semantics to a requested host.
// Under local, an empty host derives from the app name and a bare name gets
// .localhost appended, while a real FQDN is refused. Under public, a real
// FQDN is required and nothing is ever defaulted.
func ResolveHost(exposure Exposure, host, appName string) (string, error) {
	switch exposure {
	case ExposureLocal:
		if host == "" {
			host = appName
		}
		if IsLocalhost(host) {
			return host, nil
		}
		// Refuse to mangle a real FQDN into fqdn.localhost: the user meant
		// public and must say so.
		if strings.Contains(host, ".") {
			return "", ErrHostNotLocal
		}
		return host + ".localhost", nil
	case ExposurePublic:
		if !strings.Contains(host, ".") || IsLocalhost(host) {
			return "", ErrPublicHostRequired
		}
		return host, nil
	}
	return "", fmt.Errorf("cannot resolve host for exposure %q", exposure)
}
