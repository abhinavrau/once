# 14 - QR delivery: send the Recovery Kit to a phone

Status: ready-for-agent

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

- [ ] QR path serves the page over HTTPS on tailnet or LAN, save prompt
      flow works per the prototype; Done burns the token; burned/expired
      links get 410
- [ ] Token is single-use, TTL-bounded, compared constant-time
- [ ] Temporary proxy service and cert are removed on screen close
- [ ] QR renders scannably (half-block, forced colours, quiet zone); width
      guard and degraded-profile suppression covered by unit tests
- [ ] httptest coverage for the handler; no secret ever in a URL or argv
- [ ] Lint, unit, and integration tests pass
