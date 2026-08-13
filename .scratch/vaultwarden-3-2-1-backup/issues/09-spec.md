# Assemble the Vaultwarden + 3-2-1 backup spec (PRD)

Status: open
Labels: wayfinder:grilling
Blocked by: 04 (resolved), 05 (resolved), 06, 07, 08

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

(pending)
