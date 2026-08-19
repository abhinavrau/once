# PRD - Non-public routes are source-restricted at the Proxy

Feature slug: `proxy-source-restriction`. Origin: tailnet-topology ticket 06,
grilled 2026-08-18. Decision record: ADR-0016. Glossary: CONTEXT.md "Proxy",
"Local (exposure)", "Trusted Source".

## Problem

The Proxy publishes 80/443 on all interfaces and routes by Host only. A Local
app (`app.localhost`) and a Nodes-topology Tailnet app (`app.{suffix}`) answer
to anyone reaching the machine's IP with a spoofed Host header. Reproduced on
a live install: `curl -H 'Host: vaultwarden.localhost' http://<lan-ip>/` -> 200.
The UI promises "only reachable from this machine".

## Facts that shaped the design (verified)

- Docker refuses `127.0.0.1:P` when `0.0.0.0:P` is published, across
  containers (`Bind for 0.0.0.0:P failed: port is already allocated`). No
  loopback-only listener can share 80/443 with Public.
- Source address as seen by the Proxy: host loopback -> the `once` network
  gateway (`172.18.0.1` here); TSDProxy nodes -> containers on that network;
  LAN/internet -> the real client address. "Inside the `once` subnet" is the
  only discriminator on a shared port.
- On Docker Desktop and rootless Docker every connection arrives from the
  gateway: a source filter admits everyone there (today's behaviour).
- kamal-proxy `once-01` has one HTTP + one HTTPS listener, no bind address, no
  source restriction. The mechanism must be added to kamal-proxy.
- The `once` network is created once and removed only at Teardown; its subnet
  is stable for the install's life.
- `Proxy.ensureRunning` never compares the running image with the pin; a
  bumped `proxyImage` does not roll an existing install's Proxy.
- TLS at the Proxy is Public-only; Local and nodes-Tailnet routes are plain
  HTTP into it. The Recovery Handoff route deliberately serves a LAN address.

## Accepted design

- Enforce, do not restate (Q1). Local is strictly this-machine; no LAN level
  (Q2). Linux with default bridge networking is the bar; opaque-source
  platforms degrade to today, documented only, no runtime detection (Q3, Q9).
- Mechanism lives in kamal-proxy as a generic per-service source allow-list,
  `deploy --allow-from <CIDR>` (repeatable). Once passes the `once` network's
  subnet(s) for Local and nodes-Tailnet routes; Public and Recovery Handoff
  routes pass none (Q4, Q5). Rejected requests get `404`, same as an unknown
  Host (Q6).
- Interim: UI copy for Local/Tailnet is made true now and flipped back when
  enforcement ships; ADR-0016 records the decision (Q7).
- Rollout: Once rolls a Proxy running an image older than the pin (state
  volume kept) and re-registers every non-public route (Q8).
- The kamal-proxy work happens on the maintainer's own branch, published as
  `basecamp/kamal-proxy:once-02`; Once bumps the pinned tag in the same change
  that starts passing `--allow-from` (Q11).

## Tickets

- 01 - kamal-proxy: per-service `--allow-from` source restriction (ready-for-human)
- 02 - Interim: honest Local/Tailnet copy in the exposure help (ready-for-agent)
- 03 - Roll the Proxy on image-pin change and re-register non-public routes (ready-for-agent)
- 04 - Pass `--allow-from` on non-public routes and pin `once-02` (ready-for-agent, blocked by 01, 03)

## Out of scope

- A LAN reachability level or knob.
- Runtime detection of Docker Desktop / rootless.
- Refusing to mix Public with non-public apps on opaque-source platforms.
- Any change to the `services` topology.
