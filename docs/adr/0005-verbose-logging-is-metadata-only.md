# Verbose logging is metadata-only, never payloads

The `--verbose` flag logs every Docker engine call and outbound HTTP request at
debug level, as one line per request: method, URL, status, duration, error.
Request and response bodies, headers, label values, and environment variables
are never logged, because the traffic on these seams carries secrets - the
`once` label holds application encryption keys, and Tailscale API calls carry
OAuth client secrets - and verbose logs are exactly what users paste into bug
reports. Payload logging would be more useful for triage, but metadata-only
needs no redaction layer that could silently miss a new secret-bearing field:
the safety property is enforced by construction, in a single `http.RoundTripper`
that never reads bodies. Do not add payload or header logging to it.
