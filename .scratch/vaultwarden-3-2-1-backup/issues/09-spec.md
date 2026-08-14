# Assemble the Vaultwarden + 3-2-1 backup spec (PRD)

Status: done
Assignee: arau
Labels: wayfinder:grilling
Blocked by: 04 (resolved), 05 (resolved), 06 (resolved), 07 (resolved), 08 (resolved), 11 (resolved)

## Question

Write PRD.md in this directory: the build-ready spec this map exists to produce.

Pull the resolutions from tickets 01-08 into one document following the shape of
`.scratch/docktail-migration/PRD.md` (problem statement, solution, user stories,
then per-area detail). It must cover:

- The App Profile change that lets a non-Rails app deploy at all, with the
  Vaultwarden catalog entry as the worked example.
- The restic backup topology, settings, and scheduled run.
- Restore, with bare-metal recovery as the headline case.
- The acceptance test: deploy the real `vaultwarden/server` image, write data,
  back up to a local repo, copy to a second local directory standing in for
  offsite, restore from that second repo into a clean namespace, assert the vault
  is intact.

Also produce, as part of this ticket:

- **Glossary additions to CONTEXT.md** for the concepts this effort introduces
  (App Profile, Backup Repo, Offsite Copy, and whatever tickets 04-08 name),
  including `_Avoid_` lines where a synonym is tempting. Note that the existing
  **Backup / Restore** entry describes the tar.gz-to-a-file model and needs
  rewriting.
- **An ADR** for backup settings living on the proxy container's label, since it
  stretches the "proxy settings go on the proxy" rule to cover install-wide
  settings.
- **An ADR or an amendment to ADR-0013** recording that the restic password and
  the backend credentials are a second and third at-rest secret with the same
  exposure as the Tailscale credential, and stating the threat model plainly:
  the offsite copy protects against host loss, not host compromise.

Split the implementation into numbered issues in this directory, sized for an
agent session each, with a tracer-bullet first issue that gets Vaultwarden
deploying and backing up locally before the offsite and restore work lands.

## Resolution

Everything the ticket asked for is produced and committed:

- **[PRD.md](../PRD.md)** (`Status: ready-for-agent`), following the
  docktail-migration shape: problem statement, solution, 20 user stories,
  then per-area detail (App Profile with the Vaultwarden entry as the worked
  example, archive format, configuration, password custody, the Backup Run,
  restore with bare-metal as the headline, observability), testing decisions
  including the acceptance test (second repo populated by the second
  `backup` leg, per ticket 07's amendment - not `restic copy`), out of
  scope, and a release-notes section carrying the `operationDue` retry fix,
  the plain-tar export, and the Campfire naming change.
- **CONTEXT.md**: App Profile added under Core; new Backups section (Backup
  Repo, Offsite Copy, Backup Run, Staging File, Repo Password, Recovery
  Kit) with `_Avoid_` lines; the Backup / Restore entry rewritten off the
  tar.gz-to-a-file model.
- **[ADR-0014](../../../docs/adr/0014-install-wide-settings-on-the-proxy-label.md)**:
  install-wide settings ride the Proxy label as a top-level key; why
  top-level is load-bearing (labelExtra preservation), why observed state
  stays off labels.
- **ADR-0013 amended in place** (per ticket 06's choice of amendment over a
  new ADR): the at-rest inventory grows to three secrets, and the threat
  model is stated plainly - the Offsite Copy protects against host loss,
  not host compromise. Ticket 11's second amendment line (custody flow vs
  no-CLI-prompt) lands with implementation issue 13, as that ticket
  specified ("when this ships").
- **Implementation issues 12-18**, each sized for an agent session, wired
  with Blocked-by: 12 (tracer: Vaultwarden deploys and backs up locally -
  App Profile, plain tar, local run engine, interim enable), 13 (probe +
  custody screen + TUI), 14 (QR delivery), 15 (offsite leg), 16 (restore
  from repo), 17 (status + dashboard wording), 18 (acceptance test).
  Offsite and restore land after the tracer, as asked.

Fog disposition on the map: status/dashboard wording and offsite-progress
graduated into issue 17; streaming rework and restore-in-place moved to Out
of scope (beyond this map's destination; future efforts).
