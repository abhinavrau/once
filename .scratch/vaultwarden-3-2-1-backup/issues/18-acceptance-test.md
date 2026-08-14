# 18 - Acceptance test: the whole journey, real Vaultwarden

Status: ready-for-agent

**What to build:** The end-to-end integration test the map promised (PRD
"Testing Decisions"), plus a sweep for any decided-but-untested edge from
issues 12-17.

- Deploy the real `vaultwarden/server` image from the catalog. Write vault
  data through the API (attachments count as data).
- Enable backups to a local repo, with a second local directory standing in
  for offsite. Run a Backup Run; assert one snapshot per repo, populated by
  the second `backup` leg (not `restic copy`).
- Restore from the offsite stand-in into a **clean namespace** (bare-metal
  shape: nothing but the repo path and password). Assert the restored
  Vaultwarden passes the deploy gates on `/alive` and the vault contents
  match what was written.
- Assert the restored app's Profile: `/data` mount only, `DOMAIN` set,
  name `vaultwarden`.
- Keep it inside the suite's 10-minute budget (one app, small vault); no
  cloud credentials, no tailnet.

**Blocked by:** 16.

- [ ] The journey above passes in `make integration`
- [ ] Vault data integrity asserted by content comparison, not just HTTP 200
- [ ] Suite green, lint green, unit green
