Status: ready-for-agent

# Headscale integration test harness + control-plane backend abstraction

## Parent

- PRD: `.scratch/tailscale-integration/PRD.md`
- ADR: `docs/adr/0001-tailscale-integration.md`

## What to build

The shared integration-test foundation every downstream Tailscale slice depends on. Nothing about the Tailscale feature is integration-testable without a control plane that doesn't depend on Tailscale's SaaS, so this lands first.

Build a reusable test harness (extending the existing `integration/docker_test.go` namespace/proxy/deploy harness) that:

- Boots a local [headscale](https://github.com/juanfont/headscale) container (pinned tag) inside the test's Docker network as the open-source Tailscale control server.
- Creates a reusable pre-auth key (`headscale preauthkeys create --user once-test --reusable` via `docker exec`).
- Exposes a `traefik/whoami` (pinned tag) helper as the standard lightweight test app — listens on port 80, echoes its container hostname so tests can assert which backend served a request.
- Is abstracted over a **control-plane backend** — `headscale` (default) vs `tailscale-saas` — so later slices can target either. Only the headscale path is implemented here.

To prove the harness end-to-end, boot a raw `almeidapaulopt/tsdproxy:2` container directly in the test (not via Once's boot code, which doesn't exist yet) pointed at headscale via `controlUrl` + `authKey`, and assert it registers as a node. This validates that TSDProxy supports the headscale seam before Once wires it in slice 02.

## Acceptance criteria

- [ ] A test helper boots a pinned headscale container in the integration network and waits for it to be ready
- [ ] A helper mints a reusable pre-auth key for user `once-test`
- [ ] A `traefik/whoami` (pinned) helper app can be deployed via the existing harness
- [ ] The harness is structured around a control-plane backend abstraction with `headscale` as the default; the SaaS backend is a stub/seam (not implemented here)
- [ ] A smoke test boots a raw `almeidapaulopt/tsdproxy:2` container with `controlUrl`+`authKey` and asserts the node appears in `headscale nodes list`
- [ ] No assertions depend on `ts.net` HTTPS certs (headscale uses plain-HTTP `80/http:80/http` port labels)
- [ ] No Funnel scenarios anywhere in the headscale suite

## Blocked by

- None - can start immediately
