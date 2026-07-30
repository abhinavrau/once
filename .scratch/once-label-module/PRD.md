# One Module Owns the `once` Label

Status: ready-for-agent

Architecture review candidate 02. Grounded in the review of 2026-07-25 and the
preservation doctrine established by commit 3e47d59. Uses CONTEXT.md vocabulary:
Configuration, `once` label. Uses the codebase-design vocabulary: module,
interface, implementation, seam, depth, leverage, locality.

## Problem Statement

The `once` label - the single store for all of Once's Configuration - has no
owning module. Four settings types each hand-write their own codec pair:
`ApplicationSettings` (application_settings.go:84,102), `ProxySettings`
(proxy.go:45,51), `ApplicationVolumeSettings` (volume.go:25,31), and
`TailscaleSettings` (tailscale.go:168,174). Commit 3e47d59 fixed
field-destruction on label rewrite (a binary that predates a field must not
drop it when re-encoding) on exactly one of the four: `ApplicationSettings`
grew an unexported `extra` carry and a merge in `Marshal`. The helper it added,
`unknownFields()` (application_settings.go:221), is already generic over
`reflect.Type` and has exactly one caller.

The same bug shape is live on `TailscaleSettings`: two read paths -
`DomainSuffix` (tailscale.go:357) and `WaitForFunnelActive` (tailscale.go:386) -
call `persistSettings`, which recreates the once-docktail container from
`Marshal()`. `TailscaleSettings` has grown four optional fields since ADR-0002,
and a mixed-version host (newer daemon, older CLI, or vice versa) triggers the
destruction from a mere status read. `Enable` has the same hole on its rebuild
path (tailscale.go:250-288): changed credentials re-create the label from
caller-supplied settings, shedding any fields a newer binary wrote.

## Solution

One label codec module: a single file, `internal/docker/label.go`, that owns
the label key, the JSON round-trip, unknown-field preservation, and the
removed-field denylist. The four settings types embed a small unexported carry
type and become plain data; the exported per-type functions
(`UnmarshalXSettings`, `Marshal()`) keep their names and delegate to generic
`decodeLabel[T]` / `encodeLabel[T]` implementations. Preservation becomes a
property of the label, enforced at compile time, instead of a property one
struct happens to have.

## Design Decisions (settled by grilling, with evidence)

1. **Seam placement: one file in `internal/docker`, no new package.** All
   encode/decode call sites live in `internal/docker`; the only external
   callers are integration tests using the exported per-type names. A separate
   package would force an exported carry type and grow the interface for zero
   leverage.
2. **Interface: the existing exported per-type fronts stay.**
   `UnmarshalApplicationSettings`, `UnmarshalProxySettings`,
   `UnmarshalApplicationVolumeSettings`, `UnmarshalTailscaleSettings` and the
   four `Marshal()` methods remain the module's interface, delegating to
   unexported generics. Zero churn across ~25 call sites (including
   integration/tailscale_test.go and integration/docker_test.go). The fronts
   pass the deletion test: they bind per-type config (denylist, backfill) that
   would otherwise reappear at every call site.
3. **Carry mechanism: an embedded unexported `labelExtra` struct** holding
   `extra map[string]json.RawMessage`, embedded in all four settings types.
   The generics constrain on a pointer-receiver interface
   (`PT interface { *T; ... }` pattern) so a settings type that forgets to
   embed the carry fails to compile.
4. **All four types get preservation.** Proxy and volume labels are written
   only at creation today, but backup restore already round-trips
   `ApplicationVolumeSettings` through decode -> `CreateVolume`, and coverage
   is free by construction through the shared codec.
5. **`Equal` ignoring `extra` is not a bug** (evidence: `ApplySettings`'s
   short-circuit at apply_settings.go:22 skips the container roll entirely, so
   the label is not rewritten and `extra` is untouched; an older binary cannot
   edit fields it does not model). `Equal` stays field-based. Documented on the
   carry type.
6. **The persistSettings destruction fixes itself by construction.** Both
   write-through read paths feed `LoadSettings` output into `persistSettings`;
   once decode carries unknown fields and encode merges them, those call sites
   need no change.
7. **`Enable`'s rebuild grafts `existing.extra` onto the incoming settings**
   before `create`. Known optional fields (DomainSuffix, NodeHost) are still
   deliberately dropped on a credential change - the tailnet may differ - but
   unknown fields belong to a newer binary and this binary cannot judge them;
   the preservation doctrine says never destroy what you do not model.
   Staleness is the newer binary's problem to detect.
8. **The denylist stays per-type** (only `ApplicationSettings` has an entry,
   `tailscaleExcluded`); the Exposure backfill stays inside
   `UnmarshalApplicationSettings` (type normalization, not label mechanics);
   the `labelKey` const moves into label.go; the read-`Labels[labelKey]`-and-
   check-empty step stays at call sites because the empty-label policy
   genuinely differs per site (volume errors, restoreState skips, proxy
   tolerates).
9. **`unknownFields` correctness fix in the move:** skip unexported fields
   when computing which JSON keys the struct claims. Today a genuinely unknown
   JSON field named `extra` would be silently shed because the unexported Go
   field claims its name; after embedding, `labelExtra` would too.
10. **Multiple VPN providers are coming** (explicit constraint). A future
    provider adds a sibling of `TailscaleSettings`: it embeds the carry and
    adds one front pair. The codec is provider-agnostic by construction;
    nothing multi-provider is built now.

## Testing Decisions

The codec's interface is its test surface: unit tests in
`internal/docker/label_test.go` exercise the exported fronts, never the
generics directly. Unknown-field round-trip, removed-field shed, known-field
collision precedence (struct value wins), and byte-identical output when no
unknown fields exist - per settings type, without a Docker daemon. Existing
tests (`TestMarshalPreservesUnknownFields`,
`TestUnmarshalIgnoresRemovedTailscaleExcluded`,
`TestExposureBackfilledFromHostOnUnmarshal`) must pass unmodified: they pin
the behavior the move must not change. The `Enable` graft is a single
assignment behind the concrete Docker client - no unit seam exists until the
review's candidate 03 lands one; it is covered by the codec round-trip tests
plus the existing integration rebuild test staying green. Do not build a
client seam here.

## Out of Scope

- Changing when Tailscale settings persist or how mode is resolved
  (candidate 04, being designed in parallel; it will declare a dependency on
  this feature where persistence overlaps).
- A Docker client seam (candidate 03).
- Any multi-provider VPN mechanism.
- Folding per-call-site empty-label policy into the codec.
- Adding `extra` to `Equal`.

## Cross-feature dependencies

- **docker-client-seam** (candidate 03): its ticket 01 blocks ticket 01 here.
  The restore-path round-trip tests it lands pin the label decode behavior
  this codec move must not change; the review sequenced candidate 02
  downstream of that seam.

## Further Notes

- ADR: `docs/adr/0008-label-preservation-is-a-codec-property.md`.
- Domain notes: merged into `CONTEXT.md` (Label Codec entry, preservation
  note on the `once` label entry).
- Warnings convention `([]string, error)` and frontend-specific error wording
  are prior review decisions; nothing here touches them.
