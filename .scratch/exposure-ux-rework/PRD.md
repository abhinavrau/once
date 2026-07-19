# Exposure Model and Tailscale UX Rework

Status: ready-for-agent

Grounded in ADR-0003 (single exposure model) and ADR-0004 (bootstrap-only Tailscale credentials). Uses the vocabulary defined in CONTEXT.md: Exposure, App Name, Host, Bootstrap/Runtime/Policy Credential.

## Problem Statement

A user who wants to run a Docker app privately - on their own machine or on their tailnet - has to fight the product to get there. Reachability is never stated, only accumulated: the hostname's shape, a default-on `--tailscale` boolean, and a separately toggled Funnel combine into an effective exposure the user never chose, including a silent default where an app is reachable both locally and on the tailnet at once. Setting up Tailscale at all demands hand-creating OAuth clients in the admin console with the right scopes in the right order, including a throwaway policy-file credential created purely so Once can write the tailnet policy. Parts of the flow block on interactive prompts with no flag equivalents, which makes the CLI hostile to scripts and LLM agents, and some capabilities (the app catalog, bootstrap credentials) exist in only one of the two interfaces.

## Solution

Make reachability something the user states, once, in one word. Every Application has exactly one Exposure - `local`, `tailnet`, or `public` - chosen via a single `--expose` flag or a single TUI radio, with addresses derived from the App Name so the common paths need no naming decisions at all. Tailscale setup collapses to one credential: the user pastes a single Bootstrap Credential and Once mints, uses, and cleans up everything else, showing the tailnet policy changes for consent before writing them. Every interactive decision has a flag equivalent, read commands offer JSON, and the CLI and TUI expose the same capabilities, so the same product is equally operable by a human at a keyboard and an LLM agent in a script. The public path stays fully supported but unmarked: explicit, eyes-open, and never the default.

## User Stories

### Exposure

1. As a self-hoster, I want to deploy an app with no exposure flag and get a machine-local app, so that the default posture is the safest one.
2. As a self-hoster, I want to state `--expose local|tailnet|public` in one flag, so that reachability is a decision I make rather than a side effect I discover.
3. As a self-hoster, I want an app to hold exactly one exposure at a time, so that I always know precisely where an app is reachable.
4. As a self-hoster, I want a bare `--host writebook` under local exposure to become `writebook.localhost`, so that I never type a suffix the product can infer.
5. As a self-hoster, I want the host derived from the App Name when I give none, so that the common case needs zero naming decisions.
6. As a tailnet user, I want `--expose tailnet` to publish the app as a Tailscale Service named after the App Name, so that tailnet exposure costs one flag.
7. As a tailnet user, I want to override the derived service name with the same `--host` knob, so that naming works identically across exposures.
8. As a self-hoster, I want `--expose public` to fail fast when I have not supplied a real FQDN, so that I cannot publish to the internet by accident.
9. As a self-hoster, I want a warn-only DNS preflight on public deploys telling me whether my FQDN resolves and to where, so that I learn about broken DNS at deploy time instead of from a failed TLS issuance.
10. As a self-hoster, I want the DNS preflight to never block the deploy, so that deploy-then-fix-DNS remains a legitimate order of operations.
11. As a self-hoster, I want to switch an app's exposure with `update --expose`, so that promoting an app from local to tailnet is one command.
12. As a tailnet user, I want Funnel to remain a temporary, bounded window available only on tailnet-exposed apps, so that permanent public access always goes through the explicit public exposure.
13. As a tailnet user, I want `--expose tailnet` before Tailscale is enabled to fail with a clear pointer to `once tailscale enable`, so that I am directed to the fix rather than left with a broken deploy.
14. As an existing user, I want my already-deployed apps to gain an exposure derived from their hostname automatically, so that the rework does not require me to redeploy anything.
15. As an existing user, I want the removed `--tailscale` flag to fail with a message naming its replacement, so that the break is clean and self-explanatory.

### TUI

16. As a TUI user, I want the install form to present exposure as a radio choice, so that I see the same three-way decision the CLI offers.
17. As a TUI user, I want the radio preselected to tailnet when Tailscale is enabled and local otherwise, so that the likely choice is one keystroke away while remaining visible.
18. As a TUI user, I want the host field prefilled with the derived suggestion for the selected exposure, so that I can accept the default or edit it in place.
19. As a TUI user, I want a per-app Exposure settings section replacing the old Tailscale toggle, so that changing exposure after install is discoverable.
20. As a TUI user, I want choosing tailnet exposure before Tailscale is enabled to offer opening the enable overlay inline, so that I am not bounced out to the CLI.

### Tailscale setup

21. As a first-time Tailscale user, I want to create exactly one OAuth client and paste its ID and secret, so that setup is one admin-console visit.
22. As a first-time Tailscale user, I want `once tailscale enable --client-id ... --client-secret ...` to be the complete setup command, so that nothing else is required in the common case.
23. As a first-time Tailscale user, I want `--tag` to default to `tag:once`, so that I do not need to understand tags before anything works.
24. As a user with existing tag conventions, I want to override the tag with `--tag`, so that Once fits into a shared tailnet.
25. As a security-conscious admin, I want to see the exact tailnet policy changes as a diff before anything is written, so that I consent to specific changes rather than a blanket action.
26. As a security-conscious admin, I want to decline the policy write and receive a printable HuJSON snippet, so that I can apply policy through my own process (including GitOps) and no Policy Credential is ever minted.
27. As a security-conscious admin, I want the Bootstrap Credential used once and never stored, with only the narrow Runtime Credential persisted, so that the powerful secret's exposure window is minimal.
28. As a security-conscious admin, I want the one-shot Policy Credential deleted immediately after the policy write, so that no writable-policy credential outlives its use.
29. As a first-time Tailscale user, I want mis-scoped or mis-tagged credentials reported with a clear, specific error, so that I can fix the admin-console mistake without guesswork.
30. As a TUI user, I want the enable overlay reduced to bootstrap ID and secret with the tag prefilled, so that TUI setup matches the CLI's two-value minimum.
31. As a TUI user, I want the policy diff rendered in the overlay with Apply and Decline, with Decline showing the copyable snippet, so that the TUI and CLI share one consent seam.

### Scriptability and LLM-friendliness

32. As an LLM agent, I want every interactive decision to have a flag equivalent, so that no flow can dead-end me at a prompt.
33. As an LLM agent, I want `--yes` to accept the policy write and `--no-policy-write` to decline it, so that both consent branches are reachable non-interactively.
34. As a script author, I want non-TTY runs that would need a decision to fail fast with a clear message instead of hanging, so that automation fails loudly.
35. As an LLM agent, I want `--json` on `list`, `tailscale status`, and per-app status, so that I branch on structured state instead of parsing prose.
36. As an LLM agent, I want identical commands to produce identical results on every host regardless of global Tailscale state, so that my instructions are portable.
37. As a CLI user, I want `once deploy campfire` to resolve built-in catalog names to image refs, so that the catalog stops being a TUI-only capability.

## Implementation Decisions

- Exposure is stored as an explicit field in the Application's Configuration (the `once` label), one of `local`, `tailnet`, `public`. It is never derived at read time from other settings. The `TailscaleExcluded`-style boolean and the `--tailscale` flag are removed.
- Exposure determines which routing path is registered, exclusively: local and public register a host route with the Proxy (public with TLS, local without); tailnet advertises a Tailscale Service via Docktail and registers no Proxy route. No configuration can produce both.
- TLS behavior derives from exposure, not from hostname shape. The existing localhost detection remains only as input to backfill and host-suffix derivation.
- CLI default exposure is `local` unconditionally. The TUI install form preselects `tailnet` when Tailscale is enabled, `local` otherwise. This asymmetry is deliberate (ADR-0003).
- One name knob: `--host` / the TUI host field, interpreted per exposure. Local auto-appends `.localhost` to bare names; tailnet uses the value as the service name; public requires an FQDN and refuses to default. Derivation source is the App Name (from the image ref). The existing hostname-shadowing warning remains the collision guard.
- Public deploys run a warn-only DNS preflight through an injectable resolver function: unresolvable prints a warning that TLS issuance will fail until DNS points here; resolvable prints the resolved address for eyeball verification. It never blocks and adds no external IP-echo dependency.
- Backfill: on first contact with an app that has no exposure recorded, derive it from the hostname (real DNS becomes public, `*.localhost` becomes local) and write it to the label. No tailnet backfill exists; there are no Tailscale users yet (ADR-0003).
- Removed flags fail with clean-break errors naming their replacement, following the precedent set by the TSDProxy teardown message. No deprecation shims.
- `tailscale enable` accepts only the Bootstrap Credential via `--client-id`/`--client-secret`. Direct mode, its `--bootstrap-*` flag spellings, and all runtime-credential input paths are deleted (ADR-0004). `--tag` defaults to `tag:once`.
- The enable flow mints the Runtime Credential (stored) and a one-shot Policy Credential (used for the policy write, then deleted). Policy changes are planned, rendered as a diff with the GitOps warning, and applied only on consent. Declining prints the HuJSON snippet; in that branch no Policy Credential is minted.
- Consent is one seam with two renderings: the CLI prompt (with `--yes` / `--no-policy-write`) and the TUI overlay's Apply/Decline (Decline shows the copyable snippet).
- Standing rule for the whole CLI: no code path may block on a prompt without a flag equivalent, and non-TTY execution of an undecided branch fails clearly rather than hangs.
- `--json` is added to read-only commands only. `deploy` resolves catalog names (the built-in app list) to image refs before treating its argument as an image ref.
- Funnel logic is unchanged except its prerequisite becomes exposure `tailnet` instead of the advertised boolean.
- The TUI per-app settings menu replaces the Tailscale section with an Exposure section; the Funnel section remains.

## Testing Decisions

Good tests here assert external behavior at the highest seam available: what a command prints and exits with, what state Docker holds afterward, what the TUI renders - never internal function shapes. Five seams, four of them existing:

1. **CLI integration seam** (existing integration suite against real Docker): exposure defaults and flag behavior, `.localhost` auto-suffixing and App Name derivation, required-FQDN failure for public, clean-break errors for removed flags, backfill on first contact, catalog name resolution, `--json` output shape, `--yes`/`--no-policy-write` branches.
2. **TUI integration seam** (existing form drivers, driven in tab order): exposure radio with context-sensitive preselection, prefilled host suggestions, the reworked enable overlay, policy diff Apply/Decline and the snippet view. Per the standing testing note, any form-field change updates its driver in the same change, since a stale driver hangs the suite rather than failing fast.
3. **Headscale control seam** (existing): end-to-end tailnet exposure through `deploy --expose tailnet` and the enable topology, minus SaaS OAuth which this seam skips by design. Existing headscale-seam tests are rewritten from the `--tailscale` boolean to the exposure flag.
4. **httptest SaaS seam** (existing pattern): bootstrap minting, policy plan/write, and one-shot client deletion against httptest servers, byte-compatible with the real Tailscale API per the repo testing notes, since SaaS paths cannot run in CI.
5. **Injectable DNS resolver** (the one new seam): a lookup function injected into the public-deploy preflight so tests can force both resolution outcomes without real DNS.

Prior art: the existing TUI form drivers, the headscale-seam enable tests, and the byte-compatible httptest convention are all established in the repo; new tests follow their shape with testify assert/require.

## Out of Scope

- Any change to ADR-0002's architecture: Docktail, the sidecar topology, Tailscale Services as the mechanism.
- The Once Admin Web App.
- A `config get/set` surface, `--json` on mutating commands, and shell completions.
- Public-path investment beyond the DNS preflight (no reachability probing, no external IP detection, no extra confirmation ceremony).
- Funnel changes beyond the exposure prerequisite.
- Headscale as a user-facing feature (it remains a test control seam only).
- Exposure backfill for tailnet-advertised apps (no users exist to migrate).

## Further Notes

- ADR-0003 and ADR-0004 record the two load-bearing decisions and their rejected alternatives; CONTEXT.md defines the vocabulary this spec uses.
- This spec supersedes the UX aspects of docktail-migration issues 11, 16, and 17 (credential probe messaging, ACL provisioning UX, bootstrap minting UX). The mechanisms those issues built remain the substrate; their user-facing surfaces are redefined here.
- The de-prioritization of public exposure is expressed structurally (not the default, FQDN required, warn-only preflight) rather than through warnings or confirmation friction; the legitimate public user gets a first-class, unscolded path.
