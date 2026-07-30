# PRD - Docker client seam (candidate 03)

## Problem

`internal/docker` is ~9,350 lines and `Namespace.client` is a concrete
`*client.Client`, so nearly every container-touching path is reachable only
through the 10-minute integration suite. 23 distinct engine methods are called
across ~75 sites. The only unit-tested Docker paths are the two that already
sit behind narrow consumer-side interfaces: the log streamer (`logsClient`)
and the stats scraper (`statsClient`), each with a fake in its test file.
Precondition ordering in the workflow spines is asserted via a nil-pointer
panic as an accidental seam.

Two clusters of real logic are trapped behind the concrete client:

- Namespace restore: decoding `once` labels into Application and Proxy
  settings, deduplicating multiple containers per app by newest creation time,
  and capturing RunningSince. This is the read half of every label round-trip,
  and covering it is what makes candidate 02 (one module owns the `once`
  label) safe to execute.
- The sidecar crash-loop rebuild dispatch in Tailscale enable: a wedged
  sidecar (dead auth key) is removed and rebuilt with a fresh key, a stopped
  one is started, a missing one is created. No integration test can reach the
  wedged branch at all - it requires a revoked key on a real tailnet.

## Settled design

Extend the pattern the package already proves twice, instead of introducing a
package-wide client interface:

- `Namespace.client` stays a concrete `*client.Client`.
- Each path a unit test needs declares a small unexported consumer-side
  interface next to the consumer, listing only the engine methods that path
  calls. Production passes the concrete client (it satisfies the interface
  structurally - zero adapter code); the test file supplies a fake, exactly
  like the existing log and stats fakes.
- The interface is the test surface: every method on every interface is
  justified by a test that fakes it. Nothing widens speculatively.
- Where an inner step is unfakeable at the client seam at reasonable cost
  (the sidecar login wait runs `tailscale` via docker exec over a hijacked
  connection), the step itself gets an internal seam - an unexported func
  field defaulting to the real implementation - rather than dragging exec
  faking into scope. The login wait's own logic is already unit-tested behind
  `waitForSidecarLogin`.

Rejected: changing the `client` field's type to a package-private interface.
That forces a 23-method mirror of the Docker SDK (the field's static type must
carry every method called through it), most of which would never gain a second
adapter - a hypothetical seam, and a standing invitation to grow a god
interface.

Already done on main, so out of scope: the container label build
(`containerConfig`) makes no engine calls and is unit-tested; the sidecar
login wait logic is unit-tested behind fake probes; workflow precondition
ordering is unit-tested via the deliberate nil-client convention, which stays.

Also out of scope, by the two-adapter rule: deploy failure-cleanup and update
change-detection (faking them needs pull-progress streams, volumes, and proxy
exec - a god fake by the back door; the integration suite keeps those), and
any signature change to `EventWatcher`, `NewSidecarTailscaleDaemon`, or the
exec helpers until a test actually needs one faked.

## Decisions from grilling

- Consumer-side narrow interfaces; concrete field stays. (Adopted over field
  retyping; see ADR draft.)
- Restore reaches the fake by parameter: the public entry keeps its signature
  and delegates to a sibling that takes the interface; in-package tests call
  the sibling. No constructor surface added.
- Restore tests document current behavior, including the sharp edges (a
  corrupt label aborts the whole restore; inspect failures silently drop
  RunningSince). Changing that behavior belongs to candidate 02.
- Warnings convention `([]string, error)` is untouched - no new orchestration
  is added here.
- Frontend-specific error wording is untouched.
- The multiple-VPN-providers constraint does not apply: the provider-shaped
  seam is `TailscaleDaemon`, which already exists with a fake.

## Cross-feature dependencies

This feature lands AFTER candidate 01's spine tickets
(`.scratch/namespace-workflow-spine/issues/01-06`), because the spine leaves
one orchestration to test instead of two. That condition is already satisfied:
the spine landed on main before this feature was ticketed (merges 6d61e24,
6176528, eb7039f, e1e756c; commits aee4f6c, d648ebf), so both tickets are
unblocked. (`internal/docker` importing `internal/service`, decided there,
has no bearing on this seam.)

Candidate 02 (one module owns the `once` label) is downstream of ticket 01
here: the restore-side round-trip tests are what make its codec consolidation
safe. No blocking edge is declared in that direction - 02's tickets should
declare it.

## Tickets

- 01 - Unit-test namespace restore through a narrow client seam
- 02 - Cover the sidecar crash-loop rebuild without a tailnet
