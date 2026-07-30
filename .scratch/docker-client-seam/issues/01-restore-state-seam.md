# 01 - Unit-test namespace restore through a narrow client seam

**What to build:** Restoring a namespace from Docker state - the decode of
`once` labels into Application and Proxy settings, the newest-container-wins
deduplication when an app has more than one container, and the capture of
each running app's start time - should be provable in milliseconds without a
Docker daemon, because today it is only exercised by the 10-minute
integration suite and it is the read half of every label round-trip.

Follow the pattern the package already proves twice (the log streamer and the
stats scraper): declare a small unexported consumer-side interface next to
the restore path, listing only the two engine methods it calls (listing
containers, inspecting one). Production keeps passing the concrete client,
which satisfies the interface structurally; the test file supplies a fake fed
with canned container summaries and inspect responses. The public restore
entry points keep their signatures - the seam is reached by parameter from a
sibling the tests call directly.

The tests document current behavior rather than changing it, including the
sharp edges: a corrupt settings label on any container aborts the whole
restore with an error, and an inspect failure silently drops the running-since
time while keeping the app. Changing either behavior belongs to the label
module work (candidate 02), which these tests exist to make safe.

**Blocked by:** nothing - the namespace-workflow-spine tickets this was sequenced after all landed on main before this feature was ticketed (merges 6d61e24, 6176528, eb7039f, e1e756c; commits aee4f6c, d648ebf).

**Status:** in-review

- [x] A restore over canned containers decodes app settings from the `once` label and marks running apps, without a Docker daemon
- [x] Two containers for the same app restore as one Application, keeping the most recently created container's settings
- [x] The proxy container's label restores Proxy settings; a proxy with no label leaves them unset
- [x] A running app's start time is captured from inspect; an inspect failure keeps the app and drops only the start time
- [x] A corrupt settings label aborts the restore with an error
- [x] Containers outside the namespace's naming pattern are ignored
- [x] The consumer-side interface lists only the methods the restore path calls, and production still passes the concrete client
- [x] Existing unit and integration tests pass unchanged

## Comments

### 2026-07-29 - branch `docker-seam-01-restore-state-seam`

Worktree: `/home/arau/code/once/.claude/worktrees/agent-a82a35f5f2fc42d9c`

Restore now reads the engine through `restoreClient`, an unexported
consumer-side interface in `namespace.go` listing only `ContainerList` and
`ContainerInspect`; `restoreState` takes it as a parameter, so the public
entries (`RestoreNamespace`, `Refresh`) keep their signatures and pass the
concrete `*client.Client`, while the tests pass a fake. Nine unit tests in
`namespace_test.go` pin label decode, newest-container-wins dedup in both list
orders, proxy label present/absent, RunningSince capture and the silent drop on
inspect failure, the abort on a corrupt app or proxy label, list-error
propagation, and the ignoring of out-of-namespace containers. `make test` and
`make lint` are green; the integration suite is left to the coordinator's
post-merge run per the process notes (no exported signature changed).
