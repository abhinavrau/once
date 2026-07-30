# 01 - Label codec module owns the `once` round-trip

**What to build:** One file, `internal/docker/label.go`, that owns everything
about round-tripping a settings type through the `once` label: the `labelKey`
const (moves out of proxy.go:23), an unexported `labelExtra` carry struct
(`extra map[string]json.RawMessage` plus its getter/setter), a generic
`decodeLabel[T]` that unmarshals, captures unknown fields via `unknownFields`,
and sheds a per-type removed-field denylist, and a generic `encodeLabel[T]`
that marshals and merges the carried unknown fields back in (struct value wins
on collision, as today). Constrain the generics on a pointer-receiver
interface (the `PT interface { *T; ... }` pattern) so a settings type that
does not embed `labelExtra` fails to compile.

Then move `ApplicationSettings` onto it: embed `labelExtra` in place of its
bespoke `extra` field, delete the hand-written merge from `Marshal` and the
capture from `UnmarshalApplicationSettings`, and keep both exported names as
thin fronts that bind the per-type specifics - the `removedSettingsFields`
denylist and the Exposure-from-Host backfill stay in the front, not the codec.
`unknownFields` moves into label.go and gains one correctness fix: skip
unexported fields when computing which JSON keys the struct claims, so a
genuinely unknown label field named `extra` (or `labelExtra`) is preserved
rather than silently shed.

Behavior must not change: this ticket is the codec plus one already-protected
type. Existing tests pin that - in particular
`TestMarshalPreservesUnknownFields`,
`TestUnmarshalIgnoresRemovedTailscaleExcluded`, and
`TestExposureBackfilledFromHostOnUnmarshal` pass unmodified. `Equal` stays
field-based and continues to ignore the carry (settled: the ApplySettings
short-circuit skips the rewrite entirely, so nothing is destroyed); document
that on `labelExtra`.

**Blocked by:** docker-client-seam/01 (cross-feature: its restore-path
round-trip tests pin the label decode behavior this codec move must not
change - the review sequenced candidate 02 downstream of that seam).

**Status:** done

- [x] `internal/docker/label.go` owns `labelKey`, `labelExtra`, `decodeLabel`,
      `encodeLabel`, and `unknownFields`
- [x] A settings type that does not embed `labelExtra` cannot be passed to the
      codec (compile-time, via the generic constraint)
- [x] `ApplicationSettings` embeds `labelExtra`; its bespoke extra field,
      capture, and merge code are deleted
- [x] `UnmarshalApplicationSettings` and `ApplicationSettings.Marshal` keep
      their signatures and delegate; backfill and denylist live in the front
- [x] `unknownFields` skips unexported fields when computing claimed keys, with
      a test showing an unknown JSON field named `extra` survives the round-trip
- [x] Encoding a value with no unknown fields produces the same bytes as plain
      `json.Marshal`, with a test pinning it
- [x] New unit tests in `label_test.go` cover unknown-field round-trip,
      removed-field shed, and collision precedence through the exported fronts
- [x] All existing unit tests pass unmodified; `make test` and `make lint` green

## Comments

### 2026-07-30 - branch `once-label-module-01-label-codec-module`

`internal/docker/label.go` now owns the whole `once` round-trip: `labelKey`
(moved out of proxy.go), the unexported `labelExtra` carry with its
getter/setter, generic `decodeLabel`/`encodeLabel` constrained on
`labelCarrier[T] interface { *T; ... }`, and `unknownFields`. A settings type
that forgets the embed cannot reach the codec: `*X does not satisfy
labelCarrier[X] (missing method setUnknownLabelFields)`.

`ApplicationSettings` embeds the carry; the bespoke `extra` field, the capture
in `UnmarshalApplicationSettings`, and the merge in `Marshal` are gone. Both
exported names keep their signatures as thin fronts that bind the per-type
specifics - `removedSettingsFields` and the Exposure-from-Host backfill - so
the codec knows no type names. `unknownFields` gained the correctness fix: it
skips unexported fields, so a genuinely unknown label field named `extra` or
`labelExtra` is preserved instead of silently shed.

Only `ApplicationSettings` moves here; proxy, volume, and Tailscale settings
are ticket 02. Behavior otherwise unchanged - `TestMarshalPreservesUnknownFields`,
`TestUnmarshalIgnoresRemovedTailscaleExcluded`, and
`TestExposureBackfilledFromHostOnUnmarshal` pass unmodified, and no existing
test file is touched. New `label_test.go` covers the carry-named-field
round-trip, byte-identical output when there is nothing to merge, removed-field
shed, and modelled-value precedence, all through the exported fronts.

Review notes: the struct-wins branch in `encodeLabel` is unreachable through the
fronts (decode never captures a key the struct claims), kept as-is because the
ticket requires the merge semantics "as today"; its doc comment now says so.
The `labelExtra` doc no longer claims every settings type embeds it - true only
after ticket 02. `make test` and `make lint` green; integration suite not run.

- 2026-07-30 (coordinator): merged into arch-review-wave-2-tickets at 93036ec. go vet, make test, make lint, and the full integration suite (489s) green on the merged tree. Done.
