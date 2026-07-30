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

**Status:** done

- [x] `ProxySettings`, `ApplicationVolumeSettings`, and `TailscaleSettings`
      embed `labelExtra`; their fronts delegate to the codec
- [x] Unit tests in `label_test.go` show an unknown label field surviving
      decode -> `Marshal` for each of the three types
- [x] `Enable`'s rebuild path carries the existing label's unknown fields into
      the recreated once-docktail label
- [x] `Enable`'s same-credentials no-op path and field-by-field comparison are
      unchanged
- [x] Backup restore still round-trips volume settings (existing
      backup tests pass unmodified)
- [x] No `persistSettings` or `LoadSettings` call-site changes (candidate 04
      owns that ground and will build on this)
- [x] `make test` and `make lint` green; integration suite untouched or green
      if run

## Comments

### 2026-07-30 - branch `once-label-module-02-protect-remaining-labels`

All four settings types are now on the codec: `ProxySettings`,
`ApplicationVolumeSettings`, and `TailscaleSettings` embed `labelExtra`, and
their six exported fronts keep their signatures while delegating to
`decodeLabel`/`encodeLabel`. `encoding/json` left volume.go and tailscale.go
entirely. `Enable`'s rebuild grafts the decoded existing label's carry onto the
incoming settings in one assignment (`settings.labelExtra = existing.labelExtra`)
after the untouched field-by-field credential check and before `removeTopology`;
no client seam was built for it. The write-through read paths (`DomainSuffix`,
`WaitForFunnelActive`) fixed themselves by construction, and no
`persistSettings`/`LoadSettings` call site changed.

Two deviations, both deliberate:

1. The ticket says none of the three types has a removed-field denylist. That
   is wrong for `TailscaleSettings`: `apiKey` was a real Once field (added in
   ed22495, deleted in bb7c0a1) and `TestTailscaleSettingsDropLegacyAPIKey`
   pins that it is shed on rewrite. Pure binding would have resurrected a dead
   credential into every rewritten Tailscale label, so the front binds
   `removedTailscaleFields = []string{"apiKey"}` - exactly ADR-0008's "fields
   deliberately deleted from the schema go in the denylist". The ticket sentence
   and PRD decision 8 ("only `ApplicationSettings` has an entry") are stale and
   should be corrected, not the code.
2. One line of ticket 01's codec changed: `decodeLabel` now nils an unknown-field
   map that the denylist emptied. Only the denylist path could produce an empty
   non-nil carry, which broke `assert.Equal` on decoded values - the property
   this ticket requires to keep holding.

Note for whoever lands the next settings type: embedding the carry makes
`ProxySettings` and `ApplicationVolumeSettings` non-comparable, so `==` on them
is now a compile error (nothing did it; both are exported and used from
`integration/`).

New `label_test.go` cases cover an unknown field surviving decode -> `Marshal`
for each of the three types, the Tailscale case also proving `apiKey` still goes
while `futureFlag` stays, plus one case pinning that shedding every unknown
field leaves a nil carry. `make test` and `PATH` -fixed `make lint` green;
integration suite not run (coordinator runs it post-merge).

- 2026-07-30 (coordinator): merged into arch-review-wave-3-tickets at 1e6037f. go vet, make test, make lint, and the full integration suite (498s) green on the merged tree. Done.
