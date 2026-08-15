# 14 - QR delivery: send the Recovery Kit to a phone

Status: done

**What to build:** The second custody option (issues/10, 11; PRD "Password
custody"). The QR encodes a one-time HTTPS URL to a page that puts the
password in a form field - never the secret itself.

- Endpoint on the daemon's admin server beside `/up`: single-use token,
  10-minute TTL, burned by an explicit Done button (POST), screen-close
  teardown as backstop, `subtle.ConstantTimeCompare` for the token check,
  410 after burn. Page: username+password form (autocomplete username /
  new-password), POST-redirect-GET back to itself so the save prompt fires
  without changing the screen, copy buttons for the password and a Recovery
  details box (repo URL + credential names), terse copy so Done sits above
  the fold. Never served over plain HTTP.
- Routing: Tailscale enabled -> `TailnetURL("once-admin")`, existing cert.
  Otherwise mint a self-signed cert with the LAN IP in the SAN (stdlib
  `crypto/x509`), `CopyToContainer` into the Proxy (the state-file path),
  register a temporary kamal-proxy service with
  `--tls-certificate-path`/`--tls-private-key-path`, remove it on close.
  LAN IP = default route's source address (UDP dial, no packet); interface
  scan skipping docker0/br-*/veth as offline fallback.
- TUI: intro screen precedes the code (same-LAN requirement, single-use +
  TTL, cert-warning explanation, Safari blank-page-refresh quirk), then a
  dedicated full-screen QR view within 24 rows. Half-block rendering via
  `piglig/go-qr` (+1 module, MIT); force fg/bg colours (quiet zone takes the
  light-module colour), guard East-Asian ambiguous-width locales, suppress
  the QR (offer screen delivery) when the color profile is degraded.
- `--password-delivery=qr` selects it non-interactively; the custody choice
  prompt now offers both options.

**Blocked by:** 13.

- [x] QR path serves the page over HTTPS on tailnet or LAN, save prompt
      flow works per the prototype; Done burns the token; burned/expired
      links get 410
- [x] Token is single-use, TTL-bounded, compared constant-time
- [x] Temporary proxy service and cert are removed on screen close
- [x] QR renders scannably (half-block, forced colours, quiet zone); width
      guard and degraded-profile suppression covered by unit tests
- [x] httptest coverage for the handler; no secret ever in a URL or argv
- [x] Lint, unit, and integration tests pass

## Comments

Implemented on branch `issue-14-qr`. Evidence per checklist item:

- **Serves over HTTPS, Done burns, 410 after**: `internal/handoff` is the
  page and the session. `TestRecoveryHandoffServesThePageThroughTheProxy`
  (integration) drives the whole LAN route for real - boots the Proxy and
  once-admin, calls `StartRecoveryHandoff`, fetches the page through
  kamal-proxy on the minted cert, asserts the password sits in an
  `autocomplete="new-password"` field, that `/saved` is a 303 back to the
  same page which stays live, that `/done` burns, and that the next visit
  is 410. The prototype's page carried over intact, since that is the
  artifact the phone test passed against.
- **The two processes**: the custody flow runs in the CLI/TUI, but the page
  is served by the *daemon's* admin server - the tailnet route
  (`TailnetURL("once-admin")`) can only land there, so both routes converge
  on it. They share an owner-only session file in the admin runtime dir
  (`RecoveryHandoffPath`), written atomically;
  `TestAdminServerServesTheRecoveryHandoff` covers that seam over the real
  Unix socket. No new listener and no IPC protocol.
- **Single-use, TTL, constant-time**: `Store.lookup` compares with
  `subtle.ConstantTimeCompare` - the ServeMux `{token}` pattern matches any
  token, so that comparison is the whole of the check. Burn records rather
  than deletes, so a spent link and a link that never existed both answer
  410. Covered in `internal/handoff/handoff_test.go`.
- **Teardown**: `RecoveryHandoff.Close` clears the session, removes the
  `once-recovery` service and deletes the cert/key from the Proxy,
  attempting every step even after one fails. Custody fires it when the
  gate passes or the user skips; the CLI `defer`s it around the whole
  program, so ctrl+c and errors are covered too; the overlay repeats it on
  close. The integration test asserts session file gone, cert gone, route
  gone.
- **QR rendering**: `internal/ui/qr.go`, half blocks via `piglig/go-qr`
  (+1 module, MIT), 4-module quiet zone taking the light-module colour,
  both colours forced on every cell. Beyond the structural tests,
  `TestQRCodeRoundTripsToTheEncodersModules` reads the painted cells back
  into a bitmap and compares it to the encoder's own, which is what catches
  an inverted or row-swapped render. Verified out of band with a real
  decoder (OpenCV `QRCodeDetector`) against the rasterized output: decodes
  at 3-12 px/module, with and without blur.
- **Guards**: `qrUnavailableReason` suppresses the QR below 16 colours and
  in East Asian ambiguous-width locales (`LC_ALL`/`LC_CTYPE`/`LANG`
  resolved in C-library order, plus `RUNEWIDTH_EASTASIAN`). When it fires
  the delivery is simply not offered, and the prompt collapses to screen
  only. Unit-tested per profile and per locale.
- **No secret in a URL or argv**: the QR encodes `/r/<16-char base32
  token>`; the password reaches the page through the session file.
  Asserted in `TestDeployArgsCarryTheCustomCertificatePaths` and in the
  integration test's URL check.
- **TUI**: the intro screen precedes the code (same-LAN requirement,
  single-use + 10-minute expiry, the certificate warning, and the Safari
  blank-page-refresh quirk), then a dedicated full-screen QR view - 21 rows
  for a LAN URL, 23 for a tailnet one, both inside the 24-row floor and
  unit-tested at both lengths. Choice options are mouse targets
  (`delivery:screen`, `delivery:qr`) so the UI-test drivers click by name.
- **Flag**: `--password-delivery=qr` preselects the phone handoff and skips
  the prompt; the named refusal is gone. Whether a QR can actually be drawn
  is settled by `runCustodyFlow`, the first thing that knows the colour
  profile, and it errors with the reason rather than silently falling back.

Two things worth recording:

- **kamal-proxy serves the custom cert with no SNI**, which is what a phone
  dialing an IP literal does - browsers send no SNI for IP hosts. The
  prototype served the page from its own process and so never tested this;
  the integration test now dials with no ServerName set, on purpose.
- **Files copied into the Proxy must be owned by uid 1001.** kamal-proxy
  runs as its own user and cannot read a root-owned 0600 private key; the
  only symptom is `unable to load certificate`. `Proxy.copyFiles` sets the
  tar ownership.

Fixed along the way: the CLI custody program applied no palette and no
colour profile at all, so it rendered with default colours and bubbletea's
own guess. It now detects both the way `ui.Run` does - which the QR needs,
since a forced colour only survives if the profile carries it.

Three real defects came out of code review and are fixed:

- **The LAN route pointed at a container that does not exist.** It targets
  once-admin, but only the Tailscale enable lifecycle boots that - so on a
  plain install, which is exactly what the LAN route exists for, the QR led
  to a dead route. `StartRecoveryHandoff` now boots once-admin when there is
  none and destroys it again on Close, leaving the install as it found it.
  The integration test asserts once-admin is absent before and after, so it
  covers the plain install rather than the Tailscale one.
- **Degraded-profile suppression was dead in the TUI.** `ui.Run` applied the
  palette but never recorded the colour profile, so `QRUnavailableReason`
  always saw the TrueColor default and the QR was offered on terminals that
  would strip its colours. Only the CLI path had it right.
- **Quitting the TUI could strand the route.** The handoff's proxy service
  and certificate outlive the process, and ctrl+c on the QR screen left
  them behind (the TTL only expires the session, not the route). The
  Backups overlay now reports `Busy` while the page is published, through
  the same screen-to-overlay chain `ShowingSecret` already uses.

Also from review: the password was kept off the phone page's details box by
matching the label string `"Password"`, so a reworded label would have
shipped the secret into a box beside the field. It is a `secret` flag on the
row now, with a test that no detail can contain the password.

Notes for 15-18:

- `internal/handoff` is self-contained (no project imports) and
  `docker.RecoveryHandoffPath()` is the only path anyone else needs.
- `DeployOptions` now carries `TLSCertificatePath`/`TLSPrivateKeyPath`, and
  `Proxy.copyFiles` is the general "put a file in the Proxy" helper if a
  later ticket needs one.
- `ui.ColorProfile()`/`SetColorProfile` is the seam for any other feature
  that needs to know whether forced colours survive to the screen.
