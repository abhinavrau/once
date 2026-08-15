# 18 - Acceptance test: the whole journey, real Vaultwarden

Status: done

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

- [x] The journey above passes in `make integration`
- [x] Vault data integrity asserted by content comparison, not just HTTP 200
- [x] Suite green, lint green, unit green

## Comments

Implemented on branch issue-18-acceptance. The journey is
`TestVaultwardenBackupAndBareMetalRecovery`
(integration/acceptance_test.go), 34 seconds of the suite.

- **Vault data through the API.** `integration/vaultwarden_api_test.go`
  speaks the Bitwarden API a real client speaks: register, token,
  `POST /api/ciphers`, and the two-step attachment upload
  (`attachment/v2` for the slot, then the multipart PUT of the bytes).
  The endpoints were pinned against the running image before any test
  code was written - registration moved to `/identity/accounts/register`
  in current Vaultwarden and the old `/api/accounts/register` is a 404,
  which a guess would have got wrong. Values are opaque EncStrings in
  the real shape (`2.<iv>|<data>|<mac>`, MAC the right length or the
  server rejects them); the server stores ciphertext, so what matters is
  that the same bytes come back.
- **Integrity is a value comparison.** `Items()` reads the whole vault
  back - name, username, password, attachment filename, and the
  attachment's bytes fetched from its own URL - and the items written
  and the items recovered are compared with `assert.Equal`. An HTTP 200
  from a Vaultwarden with an empty database would fail it.
- **One snapshot per repo, from the second `backup` leg.** Asserted by
  two facts about the offsite snapshot, both checked against restic
  0.19.1 out of band before being relied on: `restic copy` stamps the
  source snapshot's id into `original` and **carries its timestamp
  verbatim**, so a second `backup` is the one with no `original` and a
  strictly later time. Same tree in both repos, same paths, host =
  namespace.
- **Bare metal is bare.** The source install is torn down (`Teardown`,
  volumes included) before the destination namespace is asserted to have
  no applications and `Configured() == false`. All that crosses is the
  offsite path and the password - taken **off the printed kit**, which
  with no TTY is what `backup enable` prints, and normalized the way the
  gate normalizes a typed one. Restore is from the offsite stand-in, not
  the local repo.
- **The restored Profile**: name prefixed `vaultwarden-` (not `server-`),
  Profile equal to the catalog entry's, a single `/data` mount, and
  `DOMAIN` recomputed for the new install's URL. `/alive` answers 200
  through the destination proxy, on top of the restore's own deploy
  gates.
- Also asserted along the way: the app stays served across the proxy roll
  that stores the settings, and the completion line plus the resume
  one-liner print in their specced shape.

### The sweep (issues 12-16)

- **Stale-lock recovery had no coverage at all** - the most important
  hole, since `runResticRetryLock` wraps every `cat config` and so sits
  under enable, backup and restore alike. It reached straight for the
  Docker client, so it could not be tested; the policy is now a seam of
  its own (`retryOnStaleLock`) with the mechanism injected, unit-tested
  for: no unlock without a lock failure, unlock + retry exactly once, a
  lock that outlives the unlock reported rather than looped on, and an
  unlock failure surfacing its own error.
- **ADR-0014's rationale** was asserted by nothing. An older binary is
  now simulated with a settings type that does not model the backups key
  at all, and its rewrite carries the Repo Password through; the
  counterfactual sits beside it - nested one level deeper the carry
  cannot save it, which is why the key is top-level.
- **`--health-check-path` on every deploy**: only the non-empty case was
  covered, and `deployArgs` drops an empty path.
- **`NewRecoveryKit`** was called by no test: its fields and the
  credential-names-only rule now are.
- **The export is plain tar**: `extractBackupArchive` decompresses
  transparently, so a re-added gzip writer was invisible to every
  existing assertion. The magic bytes are read off the file first.

### Notes

- **Issue 17 is not in this branch**, so no status/dashboard edge was
  swept and the journey asserts nothing about `once backup status`
  wording. When 17 lands, the natural addition is a status read on the
  recovered install: it is the one surface that can show a repo Once did
  not create.
- The ticket's "10-minute budget" predates the Makefile's explicit 30m
  suite timeout (AGENTS.md carries the current note). The test holds
  itself to a 10-minute context regardless; it uses 34s of it.
- Registry pulls remain the flakiest part of the environment: the first
  run of this test failed on `pulling proxy image: lookup
  registry-1.docker.io: i/o timeout` in the middle of `backup enable`,
  which is the IPv6 flap AGENTS.md describes, not a test defect.
