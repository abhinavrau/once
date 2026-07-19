package logging

import (
	"log/slog"
	"net/http"
	"time"
)

// WrapTransport returns a transport that logs one debug line per request:
// method, URL, status, duration, error. Never bodies, headers, or any other
// payload - see docs/adr/0005-verbose-logging-is-metadata-only.md.
func WrapTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return roundTripper{base: base}
}

type roundTripper struct {
	base http.RoundTripper
}

func (rt roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := rt.base.RoundTrip(req)

	attrs := []any{
		"method", req.Method,
		"url", req.URL.Redacted(),
		"duration", time.Since(start).Round(time.Millisecond),
	}
	if err != nil {
		attrs = append(attrs, "error", err)
	} else {
		attrs = append(attrs, "status", resp.StatusCode)
	}
	slog.Debug("http", attrs...)

	return resp, err
}
