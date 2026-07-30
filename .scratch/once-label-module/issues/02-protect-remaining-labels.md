# 02 - Proxy, volume, and Tailscale labels get preservation

**What to build:** Move the three unprotected settings types onto the label
codec, closing the live field-destruction bug on `TailscaleSettings`. Embed
`labelExtra` in `ProxySettings`, `ApplicationVolumeSettings`, and
`TailscaleSettings`; their exported fronts (`UnmarshalProxySettings`,
`UnmarshalApplicationVolumeSettings`, `UnmarshalTailscaleSettings`, and the
three `Marshal()` methods) keep their signatures and delegate to
`decodeLabel`/`encodeLabel`. None of these types has a removed-field denylist
or a backfill; the fronts are pure bindings.

That alone fixes the write-through read paths: `DomainSuffix`
(tailscale.go:357) and `WaitForFunnelActive` (tailscale.go:386) feed
`LoadSettings` output into `persistSettings`, so once decode carries unknown
fields and encode merges them back, a status read on an older binary no longer
destroys fields a newer binary wrote. Those call sites need no change.

One rewrite path does not start from a decoded value: `Enable`'s rebuild on
changed credentials or mode (tailscale.go:250) re-creates the label from
caller-supplied settings. Graft the decoded `existing` value's unknown fields
onto the incoming settings before the rebuild proceeds. Known optional fields
(DomainSuffix, NodeHost) are still deliberately dropped there - the tailnet
may differ - but unknown fields belong to a newer binary and must survive
(preservation doctrine, commit 3e47d59). The graft is a single assignment
behind the concrete Docker client; no unit seam exists for `Enable` until the
review's candidate 03 lands one, so its coverage is the codec round-trip tests
plus the existing integration rebuild test staying green. Do not build a
client seam for it here.

Constraint to respect, not build for: multiple VPN providers are coming, and a
future provider adds a sibling of `TailscaleSettings` that embeds the carry
and adds one front pair. Nothing in this ticket may assume the codec has
exactly four callers.

Watch two comparison sites: `Enable`'s stored-vs-requested credential check
(tailscale.go:243) compares field-by-field and must stay that way, and
`assert.Equal` on decoded `TailscaleSettings` values
(internal/docker/tailscale_test.go:31, integration/tailscale_test.go) still
holds because decode leaves the carry nil when no unknown fields exist.

**Blocked by:** 01.

**Status:** ready-for-agent

- [ ] `ProxySettings`, `ApplicationVolumeSettings`, and `TailscaleSettings`
      embed `labelExtra`; their fronts delegate to the codec
- [ ] Unit tests in `label_test.go` show an unknown label field surviving
      decode -> `Marshal` for each of the three types
- [ ] `Enable`'s rebuild path carries the existing label's unknown fields into
      the recreated once-docktail label
- [ ] `Enable`'s same-credentials no-op path and field-by-field comparison are
      unchanged
- [ ] Backup restore still round-trips volume settings (existing
      backup tests pass unmodified)
- [ ] No `persistSettings` or `LoadSettings` call-site changes (candidate 04
      owns that ground and will build on this)
- [ ] `make test` and `make lint` green; integration suite untouched or green
      if run
