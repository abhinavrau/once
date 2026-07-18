# 08 - Catalog names in `deploy`

**What to build:** The built-in app catalog stops being a TUI-only capability. `once deploy campfire` resolves a catalog name (the same list the TUI install screen offers) to its image ref and deploys it like any other image; arguments that match no catalog name are treated as image refs exactly as today. Catalog resolution composes with all deploy flags, including `--expose`.

**Blocked by:** None - can start immediately.

**Status:** ready-for-agent

- [ ] `once deploy <catalog name>` deploys the catalog app's image
- [ ] Non-catalog arguments behave exactly as before
- [ ] Catalog resolution works with `--expose` and the other deploy flags
- [ ] CLI integration test covers a catalog deploy
