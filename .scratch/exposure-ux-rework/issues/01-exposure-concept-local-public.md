# 01 - Exposure concept with local/public semantics

**What to build:** A user states an app's reachability in one word. `deploy`/`update` take `--expose local|public` (tailnet lands in ticket 02), defaulting to `local` unconditionally on the CLI. Exposure is stored explicitly in the app's Configuration and is never derived at read time. Under local, a bare `--host` gets `.localhost` auto-appended and a missing host derives from the App Name; under public, a real FQDN is required and the deploy fails fast without one. TLS derives from exposure, not hostname shape. Apps deployed before this change get their exposure backfilled from the hostname on first contact (real DNS becomes public, `*.localhost` becomes local) and written back to the label. `--tailscale` is removed and fails with a clean-break error naming `--expose tailnet` as its replacement. The TUI install form's conditional Tailscale checkbox is removed (the exposure radio arrives in ticket 03), with the form's integration driver updated in the same change.

**Blocked by:** None - can start immediately.

**Status:** done

- [x] `once deploy <image>` with no flags produces a local app at `{app name}.localhost` with TLS off
- [x] `--expose local --host writebook` yields host `writebook.localhost`; an already-suffixed host is left untouched
- [x] `--expose public` without a real FQDN fails fast with a clear message; with one, the app is routed through the Proxy with TLS
- [x] Exposure is stored in the `once` label and survives roll/update
- [x] An app deployed before this change gains the correct backfilled exposure on first contact
- [x] `--tailscale` errors with the clean-break replacement message
- [x] TUI install flow still passes its integration driver with the checkbox removed
- [x] CLI integration tests cover the above at the command seam
