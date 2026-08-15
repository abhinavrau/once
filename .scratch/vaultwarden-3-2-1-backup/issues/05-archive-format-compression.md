# Decision: does the restic-wrapped archive stay gzipped?

Status: done
Labels: wayfinder:grilling
Blocked by: 02 (resolved)

## Question

`backupToWriter` produces a **gzipped** tar (application_backup.go:170-172).
Feeding a compressed stream to restic largely defeats content-defined chunking:
a one-byte change early in the archive changes every subsequent compressed byte,
so daily snapshots of a slowly-changing vault may dedup close to zero. restic has
had native compression since 0.14, so gzip may be actively counterproductive.

Decide whether the archive Once hands restic stays `.tar.gz` or becomes plain
`.tar`, and confirm the numbers rather than assuming - the effect depends on
gzip's block behaviour and restic's chunker.

Considerations:

- Measure it. Two repos, ten daily snapshots of a realistic vault with small
  daily changes, gzip vs plain tar. Compare repo size and `restic backup`
  reported added bytes.
- The manual export path (`once backup <host> <file>`) writes the same stream to
  a file the user keeps. Dropping gzip makes that file much larger. Do the two
  paths diverge - compressed for manual export, uncompressed into restic - and
  what does that cost in code, given both go through `backupToWriter`?
- Restore must accept both, since repos will contain snapshots made before this
  decision if it changes later. Is the format self-describing enough
  (`parseBackup` at namespace.go:620 gunzips unconditionally), or does the
  snapshot need a tag recording its format?
- Does restic's own compression need explicitly enabling
  (`--compression auto|max|off`, repo format version 2), and what is the default
  in the pinned image?
- The staging file's size on disk is the other consequence: uncompressed staging
  needs more free disk than the compressed archive.
- **This decision is close to irreversible, per ticket 02.** The staging path is
  baked into every snapshot's `paths`, and `forget --group-by` defaults to
  `host,paths`. Renaming `/staging/<app>.tar.gz` to `/staging/<app>.tar` later
  would start a *new* retention group, leaving the old snapshots to be kept
  forever. So either settle the format now, or keep the staging filename stable
  across a format change and record the format elsewhere (a snapshot tag).
- Ticket 02 measured the repo cache for a one-big-tarball repo at **33 KiB** for
  602 MB across 8 snapshots - almost no tree metadata, because each snapshot is a
  single file node. That is the signature of the dedup concern this ticket
  exists to measure: restic is seeing one opaque blob per day, not a file tree.
  Compare that against the plain-tar variant.
- Ticket 02 also confirmed restic repo format 2 with compression available via
  `RESTIC_COMPRESSION` (`auto`/`off`/`fastest`/`better`/`max`), so the "let
  restic compress" side of this trade-off is a single env var.

## Note from ticket 03 - this is close to already answered

Ticket 03 measured exactly this while testing copy topologies. On a 20 MB tar
with a single 500 KB member changed between runs:

| staging format | bytes added on day 2 |
|---|---|
| plain `.tar` | 981 KiB |
| `gzip --rsyncable` | 1.95 MiB |
| `.tar.gz` (today's format) | **19.085 MiB - the entire archive, every day, forever** |

So the concern this ticket was opened on is real and large: gzip defeats restic's
chunker almost completely. Ticket 03 called it the decision that "dwarfs every
other cost decision" in the design.

What remains for this ticket is therefore not *whether* gzip hurts, but the
consequences already listed above: what happens to the manual export path, how
restore stays compatible with both formats, whether `RESTIC_COMPRESSION` replaces
gzip entirely, and the staging-filename stability problem. Confirm the numbers on
a realistic vault rather than a synthetic tar before locking it.

## Resolution

**Drop gzip. The archive becomes a plain `.tar`.**

The measurement decided it: gzip makes restic re-add the entire archive on every
run (19.085 MiB per day against 981 KiB for plain tar, on a 20 MB archive with
one 500 KB member changed). Keeping gzip would grow the repo by a full vault per
day and make the retention policy meaningless.

Consequences, decided together so one format exists rather than two:

- **The manual export changes too.** `once backup <host> <file>` writes plain
  `.tar`. Two formats through one `backupToWriter` would mean two code paths and
  two restore branches forever, to save disk on a file the user exports by hand
  and rarely keeps. Say plainly in the release notes that manual export files get
  bigger.
- **Restore accepts both formats.** Archives written by current Once are
  gzipped and exist on users' disks now, so `parseBackup` (namespace.go:620),
  which gunzips unconditionally, must sniff the gzip magic bytes (`0x1f 0x8b`)
  and decompress only when present. This is a few lines and it is not optional -
  without it, every existing backup becomes unrestorable on upgrade.
- **No snapshot tag records the format.** Sniffing makes the archive
  self-describing, so the tag ticket 02's grouping trap would have forced is
  unnecessary.
- **The staging path is fixed at `/staging/<app>.tar`** and never changes again,
  per ticket 02's `--group-by host,paths` trap.
- **`RESTIC_COMPRESSION` stays at its default (`auto`).** restic then compresses
  the plain tar itself, at chunk granularity, which is what preserves dedup. Do
  not set it to `off`.
- **Staging needs more free disk** than before, since the staged file is now
  uncompressed. Ticket 07 owns the free-disk check and must size it against the
  uncompressed archive.
