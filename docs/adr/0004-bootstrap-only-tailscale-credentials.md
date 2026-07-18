# Bootstrap-Only Tailscale Credentials

Enabling Tailscale originally offered two peer modes: a direct mode, where the user hand-created a three-scope runtime OAuth client plus a separate throwaway `policy_file` client just to write the tailnet policy, and a bootstrap mode, where the user supplied one client-minting (`oauth_keys` scope) credential and Once minted the rest itself. Direct mode required two to three admin-console round trips, ordered correctly around a tagOwners chicken-and-egg, with each mis-scoped checkbox a delayed failure.

We decided to support **bootstrap only**. The user creates exactly one OAuth client; `once tailscale enable --client-id ... --client-secret ...` is the whole setup. Once mints the narrowly scoped runtime credential it stores, and a one-shot policy credential it uses to write the required policy sections and then deletes. The bootstrap credential itself is used once and never stored. `--tag` defaults to `tag:once`.

The trade-off: the bootstrap credential can mint arbitrary OAuth clients, so Once briefly holds more power than direct mode ever granted it. We accepted this because the credential is transient, the stored credential is the narrow runtime one, and direct mode's friction was the single biggest obstacle to the tailnet being the low-friction path the product wants it to be. A security-maximal admin retains an escape hatch on the policy side: decline the policy write and Once prints the HuJSON snippet to apply by hand, in which case no policy credential is ever minted. There is no escape hatch for the bootstrap credential itself; supplying client-minting power is the price of admission for Tailscale support.

## Consequences

- Direct mode, its flags, docs, and TUI form branch are deleted. Re-adding it later is possible but is a full mode, not a flag.
- The enable flow must never block on a prompt without a flag equivalent: `--yes` accepts the policy write, `--no-policy-write` declines it and prints the snippet, and non-TTY runs fail clearly rather than hang.
- The TUI enable overlay renders the same policy plan diff with Apply/Decline, so CLI and TUI share one consent seam.
