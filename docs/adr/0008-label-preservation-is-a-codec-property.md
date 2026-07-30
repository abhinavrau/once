# Label preservation is a property of the codec, not a settings type

All of Once's Configuration lives as JSON in the `once` label, and a binary
must never destroy label fields it does not model: an older Once rewriting a
label (container roll, docktail recreate on a status read, enable rebuild)
would otherwise silently drop what a newer Once wrote, as it once did to
"exposure". That guarantee was first patched onto one struct
(`ApplicationSettings`, commit 3e47d59) while its three siblings kept bare
`json.Unmarshal`, leaving the same destruction live on `TailscaleSettings`
through `persistSettings`. It is now enforced by one label codec module
(`internal/docker/label.go`): every settings type embeds the codec's
unknown-field carry, and the generic decode/encode round-trip captures,
carries, and re-merges unmodeled fields, with the compile-time constraint
that a type which does not embed the carry cannot reach the label at all. The
exported per-type functions remain the interface; per-type knowledge
(removed-field denylist, decode backfills) stays in those thin fronts, so the
codec itself knows no type names. Fields deliberately deleted from the schema
go in the denylist; everything else unknown survives every rewrite, including
`Enable`'s rebuild, where stale-but-preserved beats destroyed - only the newer
binary that owns a field can judge its staleness. `Equal` intentionally
ignores the carry: an equality short-circuit skips the rewrite entirely, and
an older binary cannot edit fields it does not model. Multiple VPN providers
beyond Tailscale are coming; a provider's settings type is one more embedder
and one more front pair, so do not special-case the codec for Tailscale, and
do not hand-write a codec for a new settings type.
