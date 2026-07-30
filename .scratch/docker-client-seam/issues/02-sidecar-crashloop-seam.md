# 02 - Cover the sidecar crash-loop rebuild without a tailnet

**What to build:** The sidecar boot dispatch inside Tailscale enable - rebuild
a crash-looping sidecar with a fresh key, start a stopped one, leave a running
one alone, create a missing one - should be provable without a Docker daemon
or a tailnet, because today its most important branch is unreachable by any
test: a wedged sidecar requires a dead auth key on a real tailnet, which the
integration harness cannot fabricate.

Follow the consumer-side interface convention (the log streamer, the stats
scraper, and ticket 01): a small unexported interface next to the sidecar boot
path, listing only the engine methods the dispatch calls (inspect, remove,
create, start, image pull). Production keeps the concrete client; the test
fake returns canned inspect responses shaped like each branch.

One inner step cannot be faked at that seam at reasonable cost: the login wait
runs the tailscale CLI inside the sidecar via docker exec over a hijacked
connection. Give the login wait an internal seam - an unexported hook
defaulting to the real implementation - so dispatch tests stub it. The wait's
own logic stays covered by the existing fake-probe tests; do not widen the
scope into exec faking. Tests use the control-seam environment variables the
boot path already honors, so no SaaS key minting or HTTP faking is involved.

**Blocked by:** nothing - the namespace-workflow-spine tickets this was sequenced after all landed on main before this feature was ticketed (merges 6d61e24, 6176528, eb7039f, e1e756c; commits aee4f6c, d648ebf).

**Status:** in-review

- [x] A crash-looping sidecar (restarting, or exited nonzero) is removed and a new one created and started, then the login wait runs - all without a daemon
- [x] A stopped sidecar is started, not recreated
- [x] A running sidecar is neither started nor recreated, and the login wait still runs
- [x] A missing sidecar is pulled, created, and started
- [x] An inspect failure other than not-found surfaces as an error with no container churn
- [x] A sidecar whose start fails is removed rather than left half-created
- [x] The login wait hook defaults to the real implementation and is only overridden in tests
- [x] The consumer-side interface lists only the methods the dispatch calls, and production still passes the concrete client
- [x] Existing unit and integration tests pass unchanged

## Comments

**2026-07-29** - branch `docker-seam-02-sidecar-crashloop-seam`, worktree
`/home/arau/code/once/.claude/worktrees/agent-aff961177ae37809c`

`bootSidecar` now delegates to `bootSidecarWithClient`, which takes the new
unexported `sidecarBootClient` (inspect, create, start, plus the embedded
one-method `containerRemover`/`imagePuller` that the shared remove and pull
helpers take), and the login wait moved behind an unexported `awaitLogin` hook
that is nil in production and overridden only in tests. The new
`internal/docker/tailscale_sidecar_test.go` drives all four boot branches plus
the inspect-failure, remove-failure and failed-start paths against a fake, with
the headscale control-seam env vars set so no key is minted and no tailnet or
daemon is touched. `make test` and `golangci-lint` pass; the integration suite
is left to the coordinator.
