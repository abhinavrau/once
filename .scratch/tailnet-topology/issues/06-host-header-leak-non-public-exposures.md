# 06 - Non-public exposures answer to spoofed Host headers on the public IP

**Problem:** The Proxy publishes 80/443 on `0.0.0.0`. A Local app
(`app.localhost`) is routed by Host header only, so anyone reaching the
server's public IP with `Host: app.localhost` gets the app. ADR-0015 routes
Nodes-topology tailnet apps via the Proxy too, on `{app}.{suffix}`, and
accepts that they inherit the same hole for now. "Reachable only from this
machine / only from the tailnet" is Host-header obscurity today.

**Not part of the tailnet-topology feature.** Recorded so it is not lost.
Candidate fixes to grill: a second Proxy listener not published on host
ports (Local + Tailnet apps route there; Public stays on the published one);
kamal-proxy source-IP restrictions if it grows them; publishing the Local
listener on 127.0.0.1 only. Needs a decision on whether Local via 127.0.0.1
breaks anyone's LAN usage.

**Blocked by:** Nothing; separate feature.

**Status:** needs-triage
