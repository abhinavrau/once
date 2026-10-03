Status: done

# Two namespaces on one host hijack each other's tailnet nodes

## Parent

- Observed live: test apps joined the user's real tailnet through the live
  `once-tsdproxy`, and nodes integration tests failed intermittently
- Decision: [ADR-0018](../../../docs/adr/0018-tsdproxy-serves-only-its-own-namespace.md)

## What's wrong

TSDProxy (v2.3.4, Nodes topology) watches every container on the Docker host
labelled `tsdproxy.enable=true` and keys proxies by `tsdproxy.name`. With two
Once namespaces on one host:

- each namespace's TSDProxy serves the other's apps in its own tailnet;
- a container starting or dying under a name the other namespace also uses
  closes the other's proxy. Live log of the live `once-tsdproxy` while the
  integration suite ran: `stopping proxy name=once-admin` ... `proxy stopped`;
- `TestTailscaleEnableBootsNodesTopology`,
  `TestTailscaleRoundTripsBetweenNodesAndServices` and
  `TestTailscaleEnableNodesOnFreshInstallBootsProxy` fail with
  `not yet advertised by the local tailscaled: once-admin` whenever the live
  install's `once-admin` is running.

## Acceptance criteria

- [x] Every container TSDProxy should serve carries
  `tsdproxy.proxyprovider=<namespace>`; the namespace's config defines only that
  provider plus an unusable default, so other namespaces' and unlabelled
  containers are refused without closing any existing proxy
- [x] Pre-existing node state under `/data/default` is linked to the new
  provider before TSDProxy starts, idempotently and atomically, so every node
  keeps its name
- [x] Existing installs adopt it at daemon startup (and on `once tailscale
  enable`): a non-isolating `once-tsdproxy` is rebuilt, and apps and
  `once-admin` lacking the provider label are rolled
- [x] Integration test with two namespaces on one host, same-named apps and
  `once-admin`, asserting neither TSDProxy serves the other's containers and
  tearing one down leaves the other's proxies running
- [x] Integration test of the heal carrying legacy state over with unchanged
  node names

## Follow-up

- The Services topology (Docktail) has the same cross-namespace problem:
  Docktail watches every container's `docktail.*` labels and documents no
  filter (https://docktail.org/llms-full.txt, Environment Variables). Needs its
  own ticket; not fixed here.
- The app-roll half of the heal is not integration-tested: standing in an app
  without the provider label would let the live install's pre-isolation
  TSDProxy serve it on this host. It is covered by `carriesTailnetLabels`'
  unit test and by the heal integration test's no-op second heal.
