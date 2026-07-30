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

**Status:** ready-for-agent

- [ ] `internal/docker/label.go` owns `labelKey`, `labelExtra`, `decodeLabel`,
      `encodeLabel`, and `unknownFields`
- [ ] A settings type that does not embed `labelExtra` cannot be passed to the
      codec (compile-time, via the generic constraint)
- [ ] `ApplicationSettings` embeds `labelExtra`; its bespoke extra field,
      capture, and merge code are deleted
- [ ] `UnmarshalApplicationSettings` and `ApplicationSettings.Marshal` keep
      their signatures and delegate; backfill and denylist live in the front
- [ ] `unknownFields` skips unexported fields when computing claimed keys, with
      a test showing an unknown JSON field named `extra` survives the round-trip
- [ ] Encoding a value with no unknown fields produces the same bytes as plain
      `json.Marshal`, with a test pinning it
- [ ] New unit tests in `label_test.go` cover unknown-field round-trip,
      removed-field shed, and collision precedence through the exported fronts
- [ ] All existing unit tests pass unmodified; `make test` and `make lint` green
