# The Tailscale SaaS API is reached through one Tailscale-specific adapter

Once talks to two Tailscale surfaces: the local daemon, behind the
TailscaleDaemon seam, and the SaaS control-plane HTTP API, which used to be
reached by threading a baseURL string - with exactly one production value -
and a hand-fetched bearer token through every flow. The SaaS surface now sits
behind a single unexported seam in the Docker module: a one-method interface
whose authenticated Call takes method, path, and payload and returns status
and a bounded body. Its HTTP adapter owns the production base URL, the OAuth
client-credentials exchange (performed lazily on first call and cached for
the instance, which lives one operation), the bearer header, and the bounded
response read. Request payloads, paths, and status-to-error wording stay in
the flows, because several requests must remain byte-compatible with what
docktail sends at runtime and error wording is flow-specific by decision; the
adapter transports bytes, it does not interpret them. The adapter sits above
the metadata-only logging RoundTripper (ADR-0005) and never replaces it: it
performs no logging of any kind, so verbose logging stays metadata-only by
construction. Multiple VPN providers are coming, and this seam is exactly
where a second provider's control-plane traffic would live - but the adapter
is deliberately Tailscale-specific in name, path shapes, and auth, and a
second provider gets a sibling adapter of its own. Do not generalize it into
a provider interface until a second provider actually exists: one provider
means the provider seam is still hypothetical. Tailscale SaaS OAuth cannot
run in CI, so the exchange is covered by httptest unit tests against the HTTP
adapter, and the flows are tested through a fake adapter that routes canned
JSON through the production parsers so the two cannot drift.
