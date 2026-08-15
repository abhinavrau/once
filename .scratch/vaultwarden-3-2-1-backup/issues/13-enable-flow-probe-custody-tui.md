# 13 - Enable flow: validation probe, custody screen, TUI surfaces

Status: done

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

- [x] Enable runs probe before custody; probe failure persists settings +
      password, leaves `Enabled` false, shows the error; `once backup
      status` shows configured-but-not-enabled with the last error
- [x] Probe leaves no snapshot behind; repeated failed probes land in one
      retention group
- [x] Custody: variant D within 80x24, type-back gate normalizes case/
      dashes/spaces, skip hatch behaves as specced, ctrl+p/ctrl+k copy
- [x] No-TTY enable prints banner and never prompts;
      `--password-delivery=screen` skips the choice
- [x] TUI overlay, per-app checkbox, and inline-overlay-on-install work;
      form drivers updated; custom-image install always asks health path
- [x] ADR-0013 amendment line added
- [x] Lint, unit, and integration tests pass

## Comments

Implemented on branch issue-13-enable-flow. Evidence per checklist item:

- Enable sequence + failure semantics: `ConfigureBackups` (persist with
  Enabled withheld, probe, record in `State.LastProbe`) and `EnableBackups`
  in internal/docker/backup_probe.go, driven by both frontends. Integration
  `TestBackupEnableProbeFailureWithholdsEnabled`: a wrong-password local
  repo fails the probe fast (no backend-retry backoff), settings + password
  persist, `Enabled` stays false, `once backup status` output asserted
  ("configured but not enabled" + "Last validation: failed"), and the retry
  reuses the persisted password.
- No snapshot behind / one retention group:
  `TestBackupEnableDisableCLIPersistsSettings` asserts empty snapshot
  groups after a successful enable; `TestResticProbeBackupArgsAreConstant`
  pins the fixed `--host` + `/staging/.once-probe` path.
- Custody: shared `ui.Custody` (internal/ui/custody.go, prototype variant D
  trimmed - kit block loses its vertical padding and subtitle). Unit tests:
  80x24 fit, gate normalization, mismatch, skip hatch, ctrl+p/ctrl+k,
  ShowingSecret only on reveal. Integration `TestUIBackupsOverlayEnableFlow`
  drives the whole overlay sequence and types the password back lowercase
  with dashes.
- No-TTY: the enable integration tests run without a TTY - banner printed,
  no prompt, `Enabled` set. `--password-delivery` unit-tested: screen
  accepted, qr a named refusal until 14.
- TUI: dashboard `b` overlay (internal/ui/backups_form.go, TailscaleForm
  shape, growing credential pairs); install checkbox on both paths with
  inline-overlay-once (`TestUIInstallBackupCheckboxOffersOverlayOnce`);
  custom-image install asks data path + health path unconditionally, the
  checkbox forces the data path (unit tests). Mouse tracking off on
  secret frames via the `SecretDisplayer` chain (App -> screen -> overlay
  -> custody). Drivers gained `clearField` (ctrl+u).
- Same-device warning: `system.SameDevice` + `Namespace.BackupOnDataDevice`,
  surfaced in `once backup status` (dashboard wording stays with 17).
- ADR-0013 amendment line sits under its no-CLI-prompt bullet.

Notes for 14-16:

- 14 (QR): `custodyDeliveries` in internal/ui/custody.go is the seam -
  append the second option and the choice prompt reappears (`Custody`
  already tracks `choice`); replace `validatePasswordDelivery`'s named
  refusal of `qr` in internal/command/backup.go.
- 15 (offsite leg): `offsiteResticRepo` exists and the probe already
  exercises it end to end; its init passes `--copy-chunker-params` from
  the local repo via `RESTIC_FROM_REPOSITORY`/`RESTIC_FROM_PASSWORD`, so
  the run's offsite leg is one more `backup` invocation plus the
  log-don't-fail semantics.
- 16/17: `State.LastProbe` and `Namespace.BackupOnDataDevice` are the new
  status surfaces; `captureCLIOutput` in integration/cli_test.go captures
  plain-text CLI output for wording assertions.
