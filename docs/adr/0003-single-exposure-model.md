# Single Explicit Exposure per Application

Reachability of an application used to fall out of three independent knobs: the shape of the hostname (`*.localhost` vs real DNS), a per-app `--tailscale` boolean (default true when Tailscale was globally enabled), and Funnel toggled separately. Nobody ever stated "this app is tailnet-only"; they arranged knobs until it was true, and the default combination silently exposed an app both locally and on the tailnet at once.

We decided to make **Exposure** a first-class concept: `local`, `tailnet`, or `public`, exactly one per application, chosen explicitly (`--expose` on the CLI, a radio in the TUI) and stored in the app's `once` label. Funnel is not a fourth exposure; it remains a temporary, bounded public window available only on top of `tailnet`.

The app name is the identity and each exposure derives its address from it: `{app}.localhost` under local (suffix auto-appended), the Tailscale Service name under tailnet, and under public a real FQDN is required with no default. One name knob, interpreted per exposure.

Defaults differ by interface deliberately: the CLI always defaults to `local`, regardless of global Tailscale state, so identical commands behave identically on every host (predictable for scripts and LLM agents, secure by default); the TUI preselects `tailnet` when Tailscale is enabled because a human sees the choice on screen, making a context-sensitive default convenience rather than magic.

## Consequences

- An application cannot hold two exposures at once. The previous accidental local+tailnet double exposure is no longer representable.
- `--tailscale` is removed as a clean break, replaced by `--expose tailnet`.
- Existing apps get their exposure backfilled from the hostname on first contact (real DNS becomes `public`, `*.localhost` becomes `local`). No tailnet backfill exists because there were no Tailscale users at the time of this decision.
- `public` deploys run a warn-only DNS preflight (resolve the FQDN, report what it points to); it never blocks, because deploy-then-fix-DNS is a legitimate order of operations.
