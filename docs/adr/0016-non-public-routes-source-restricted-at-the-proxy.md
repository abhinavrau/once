# Non-public routes are source-restricted at the Proxy

Extends [ADR-0003](0003-single-exposure-model.md) (Exposure is the one
reachability knob) and closes the hole [ADR-0015](0015-tailnet-topology-seam-nodes-default.md)
accepted for Nodes-topology tailnet apps.

The Proxy publishes 80/443 on every interface and picks an Application by Host
alone, so a Local app (`app.localhost`) and a Nodes-topology Tailnet app
(`app.{suffix}`) both answer to anyone who reaches the machine's IP with a
spoofed Host header. "Reachable only from this machine / only from the tailnet"
was Host-header obscurity. We considered binding a second listener to
`127.0.0.1` for non-public routes and rejected it on a hard fact: Docker
refuses to publish `127.0.0.1:P` while `0.0.0.0:P` is held, even across
containers, and Public must keep 80/443 on all interfaces. Moving Local to
other ports, a host-network Proxy, or a hop container in front of kamal-proxy
were all heavier than the problem.

We decided the Proxy enforces reachability by **request source**, and that
this lives in kamal-proxy, not around it. A non-public route (Local, and
Tailnet under `nodes`) is deployed with an allow-list of source CIDRs - the
`once` Docker network's subnet, whose gateway is where the host's own loopback
traffic arrives and where TSDProxy nodes live as containers. A request from
any other source gets the same `404` an unknown Host gets, so the app's
existence is not confirmed. Public routes and the Recovery Handoff route (which
deliberately serves a LAN address) carry no allow-list. The policy (which
routes are restricted, and to what) stays in Once; kamal-proxy gains only the
generic per-service mechanism. Local is strictly this-machine: a LAN client
that reached a Local app through a hosts-file entry was using a bug, not a
feature, and has Tailnet for that need.

Consequences and accepted costs:

- Enforcement is real on Linux with Docker's default bridge networking, where
  the Proxy sees true source addresses. On Docker Desktop and rootless Docker
  every connection arrives from the gateway, so the filter admits everyone
  there - today's behaviour, not worse. Accepted: the threat surface is Linux
  servers on the internet. Documented, not detected at runtime.
- Existing installs must be brought under the rule, not just new deploys: Once
  rolls a Proxy whose image is older than the pin (kept its state volume, as
  `UpdateSettings` already does) and re-registers every non-public route.
- Until the kamal-proxy build with the feature exists, the Local/Tailnet
  promise in the UI copy is stated honestly ("not advertised; answers only at
  its Host") and flipped back when enforcement lands.
- The `services` topology is untouched: Docktail never routes through the
  Proxy.
