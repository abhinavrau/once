# 02 - Docktail serves every namespace's Tailscale Services

**Status:** needs-triage

**What to build:** Under the Services topology, a namespace's Docktail only
advertises its own namespace's containers, matching what ticket 01 / ADR-0018
gave the Nodes topology.

## What is wrong

Docktail watches every container on the Docker host carrying `docktail.*`
labels, with no filter by label value or Compose project. Two Once namespaces on
one host in the Services topology each advertise the other's apps, and two
same-named apps (e.g. both namespaces' `once-admin`) compete for the same
`svc:` name. The integration suite's Services tests can therefore touch a live
install's tailnet the same way the Nodes tests did before ticket 01.

## Acceptance criteria

- [ ] Checked against Docktail's source/docs (https://docktail.org/llms.txt):
      is there a label filter, project scope, or per-instance selector? If
      none, decide between an upstream change, a per-namespace label prefix,
      or documenting Services as single-namespace-per-host.
- [ ] Integration test with two Services-topology namespaces on one host,
      asserting neither Docktail advertises the other's containers.
- [ ] Safe to run with a live install on the host.

## Comments
