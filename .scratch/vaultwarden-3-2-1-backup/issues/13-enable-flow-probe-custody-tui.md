# 13 - Enable flow: validation probe, custody screen, TUI surfaces

Status: ready-for-agent

**What to build:** Replace issue 12's interim enable with the real sequence:
**generate password -> validation probe -> custody flow -> `Enabled = true`**
(issues/07, 11; PRD "Password custody"). The QR delivery option is issue 14 -
here the choice prompt offers only "show on screen" (collapsing the prompt),
built so 14 adds the second option without restructuring.

- Synthetic validation probe, bounded O(1): write
  `<LocalPath>.staging/.once-probe`, `cat config`/`init` per configured repo,
  `backup` it with the real `--host` and a fixed path, `forget` without
  `--prune`. No `check`/`prune`. On failure persist everything including the
  password, withhold `Enabled`, report the restic error verbatim, keep the
  form populated. On retry the existing password is reused, never
  regenerated.
- Custody flow as one shared bubbletea program run sequentially by both
  frontends (the `cli_progress.go` pattern; prototype on branch
  `prototype/password-moment`, final commit 5429b0b). Reveal is variant D:
  copy / store / prove checklist with the ONCE RECOVERY KIT block (password
  grouped in 4s, offsite URL, credential names, local path, created),
  trimmed to 80x24. ctrl+p password, ctrl+k kit (OSC52); mouse tracking off
  on secret-bearing screens. Unmasked normalizing type-back gate; esc = loud
  skip confirm (password persisted, `Enabled` false, finish via
  `once backup enable`).
- No TTY: print password + banner, no gate. `--password-delivery=screen`
  flag accepted now (`qr` arrives with 14).
- TUI: dashboard-global Backups overlay (the `TailscaleForm` shape) with
  local path, offsite URL, credential name/value pairs, and the custody
  moment; per-app Settings > Backups reduced to the participation checkbox +
  last-run line; install flow (catalog and custom image) asks the checkbox,
  and ticking it with no repo configured opens the overlay inline, once.
  Custom image: the checkbox forces `DataPath`; the health path is asked
  unconditionally. Update the UI-test drivers in the same change.
- Same-device warning: `stat` the Docker root vs `LocalPath` device; surface
  in status (dashboard wording in 17).
- ADR-0013 gets its one amendment line: reading a secret in stays TUI-only;
  handing one out reuses the shared custody component under the CLI's
  sequential-program pattern.

Offsite fields are configurable here (form + flags + probe cover them), but
the run's offsite leg is issue 15.

**Blocked by:** 12.

- [ ] Enable runs probe before custody; probe failure persists settings +
      password, leaves `Enabled` false, shows the error; `once backup
      status` shows configured-but-not-enabled with the last error
- [ ] Probe leaves no snapshot behind; repeated failed probes land in one
      retention group
- [ ] Custody: variant D within 80x24, type-back gate normalizes case/
      dashes/spaces, skip hatch behaves as specced, ctrl+p/ctrl+k copy
- [ ] No-TTY enable prints banner and never prompts;
      `--password-delivery=screen` skips the choice
- [ ] TUI overlay, per-app checkbox, and inline-overlay-on-install work;
      form drivers updated; custom-image install always asks health path
- [ ] ADR-0013 amendment line added
- [ ] Lint, unit, and integration tests pass
