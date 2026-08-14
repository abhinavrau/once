# Research: can a terminal show a scannable QR code for the repo password?

Status: done
Labels: wayfinder:research
Blocked by: none

## Question

Once shows the restic repo password exactly one time. If the user does not
capture it, bare-metal restore is impossible forever. Typing a 64-character
secret from a terminal into a phone password manager by hand is the failure
point. A QR code the user scans with a phone would remove the typing.

Establish whether this is actually possible, and what it should encode.

- **Is a terminal QR code scannable in practice?** Terminal cells are roughly
  1:2 (width:height), so a naive one-cell-per-module render is stretched and
  fails. The half-block (`▀`/`▄`) technique packs two module rows per text row
  and gives square modules. Confirm this scans on a real phone camera, and find
  the minimum terminal size for the payload sizes below.
- **What does it encode?** Two candidates:
  - the password itself, as text. No server, no network, works on a headless
    box. The secret is already on screen, so the QR adds no new exposure.
  - a URL to a page Once serves (the admin server already exists,
    internal/docker/admin.go). The phone must be able to reach that URL:
    `localhost` is unreachable from a phone, so this only works over the
    tailnet, and only when Tailscale is enabled. Establish whether that is a
    real path or a dead end.
- **Library.** Find a pure-Go QR encoder (CGO is disabled - see the Makefile)
  and whether anything in the module graph already provides one. Report module
  path, license, maintenance state, and whether it emits a module bitmap
  directly rather than a PNG we would have to decode.
- **Rendering constraints.** QR needs a quiet zone and correct polarity. Once's
  TUI supports light and dark palettes (internal/ui/palette.go), so establish
  what the code must draw to stay scannable on both, and whether it must force
  its own foreground/background rather than inherit the theme.
- **Scope check.** Report anything that makes this not worth doing: terminals
  that mangle half-blocks, size floors that exceed a normal window, phone
  password managers that refuse plain-text QR payloads.

## Resolution

Full note: [docs/research/terminal-qr-secret-handoff.md](../../../docs/research/terminal-qr-secret-handoff.md)

**Technically yes, and cheaply - but gate it on a five-minute phone test first,
because the payoff is the part that is in doubt, not the feasibility.** Verified
with a real decoder (OpenCV `QRCodeDetector`) against half-block renders
rasterized the way a terminal paints them, then pushed through a camera model
(resample, defocus, noise). Not verified with a phone camera - nobody here has
one.

- **Half-block works, naive rendering does not.** The half-block render decoded
  at every terminal cell metric tested (7x15 through 10x24), including cells
  that are *not* exactly 1:2 - 8x17 and 9x19 give modules 5-6% off square and
  still decode. One-cell-per-module failed universally. Well-trodden path:
  `libqrencode -t UTF8` uses the same U+2580/U+2584/U+2588 glyphs and the same
  4-module default margin, and **`tailscale up --qr` already ships exactly this**
  (half blocks via `skip2/go-qrcode`, plus a `GIO_UNIMAP` probe confirming the
  console font actually has the glyphs).
- **The binding constraint is the 80x24 floor, and the password's alphabet
  decides it.** 64 mixed-case chars at EC level Q renders **49x25 - does not fit
  at all**. The same 64 chars as **uppercase base32 (A-Z 2-7)** render
  **41x21**, because QR switches from byte mode to alphanumeric mode. Same
  entropy budget (320 bits), two QR versions smaller, level Q for free. **Decide
  the alphabet before the password format is fixed** - retrofitting it later
  means changing the format for existing installs. Cross-checked with two
  independent encoders, both matching the ISO/IEC 18004 capacity table.
- **The URL option is a dead end.** `internal/docker/admin.go` gives `once-admin`
  **no host ports**; it is a Unix-socket nginx container published only as a
  Tailscale Service. No localhost path (a phone cannot reach the server's
  localhost), no LAN path. It works only with Tailscale already enabled, which
  at configure time on a fresh headless box is exactly when it usually is not.
  Do not build it.
- **Polarity must be forced, and degradation must be detected.** An inverted
  symbol is spec-legal (ISO 18004 clause 6.2 permits reflectance reversal; clause
  12 tells decoders to retry inverted), but ZXing only does so behind an opt-in
  `ALSO_INVERTED` hint and ZXing's own Android app does not set it - so it is a
  coin flip on the user's phone, and it decoded as FAIL here. Half-blocks need
  *both* fg and bg per cell, and `internal/ui/app.go:302-305` only forces
  truecolor when detected - under a degraded profile the colors get stripped and
  the code renders inverted on a dark theme. Force colours the way
  `qrencode -t ANSIUTF8` does, and **suppress the QR when the profile is degraded
  rather than draw an unscannable one.** Also: the quiet zone must take the
  *light module* colour (clause 6.3.8), not "white".
- **Two hazards I nearly missed.** (a) U+2580/U+2584/U+2588 are East Asian Width
  **Ambiguous**, so a CJK locale or an ambiguous-width=wide setting doubles their
  width and guarantees a stretched, over-wide, unscannable code - needs a width
  guard. (b) The seam failure is driven by **line height, not fonts**: over ~1.15
  nothing decodes at any distance, and counterintuitively *more* camera
  resolution makes a seam worse (blur hides it below ~4 px/module, the sensor
  resolves it above). Zed #17904 is this exact feature failing on a golden-ratio
  line height. Terminal defaults are mostly safe now since every major terminal
  draws block elements itself.
- **Library: `github.com/piglig/go-qr`.** MIT, maintained (v1.1.0, commits
  through 2026-08), pure Go, `Size()` + `Module(x, y) bool`. Its only requirement
  is the testify chain `once` already has, so the real cost is **+1 module, +2
  `go.sum` lines**. Nothing in the module graph provides a QR encoder today.
  Rejected `skip2/go-qrcode` (unmaintained since 2020, plus a latent
  `penalty`/`q.symbol` bug in `encode()` that makes `DisableBorder` silently
  order-dependent), `rsc.io/qr` (frozen 2018), and `mdp/qrterminal` (+3 modules,
  ANSI escapes that fight lipgloss, half-height bottom margin row).
- **Non-issues, checked:** capture resolution (decodes down to ~3 px/module; a
  laptop screen at reading distance is nowhere near the floor), contrast
  (Michelson 0.08 still decoded), quiet zone in a cramped TUI (1 module still
  decoded, but keep all 4).
- **Real risks:** the 80x24 row budget, which forces the alphabet choice and a
  **dedicated full-screen QR view** (21 of 24 rows leaves no room to share a
  screen with the password text and the type-it-back confirmation); terminals
  that leave a multi-pixel seam between block glyphs (a 1px seam is rescued by
  camera blur, 2px+ is fatal).

**The catch, and it is the reason to gate rather than just build.** The one
payload type stock phone scanners act on well is a URL - `https://`,
`otpauth://` and `WIFI:` are the whole list - and that is precisely the one Once
cannot serve at the moment the password is shown. For a raw text payload:

- **No password manager ingests it.** Bitwarden's scanner exists only under "Set
  Up TOTP"; 1Password is TOTP-only for item data; Apple Passwords imports
  CSV/ZIP; Google Password Manager imports `.csv`. All four: no raw-text QR path.
- **Apple documents no copy affordance for plain text at all** - every Apple
  description of the result is "tap the link". Absence of documentation, not a
  documented failure, but that is the whole of the first-party evidence.
- **Android says "view text", not copy**, and ML Kit's parsed-type list excludes
  plain text entirely (it returns `rawValue` for the app to handle).

So the honest framing is: the engineering is cheap and correct, but the feature's
*value* rests on an undocumented phone behaviour. One person with a phone can
settle it in five minutes - render a plain-text QR, scan it on a stock iPhone and
a stock Android, see whether the string reaches the clipboard. If yes, build it
(base32 alphabet, own view, ~a day). If no, spend the same day on the password
screen itself - chunked rendering, a real "write this down" gate, the
type-it-back confirmation ticket 06 already specifies.

Do **not** work around it by wrapping the secret in an `otpauth://` URI to force
manager ingestion - it would be a lie about the secret's type, the manager would
derive rotating TOTP codes from it, and the original string would be
unrecoverable from the manager's UI.

Hand to ticket 11 (prototype): **do the phone test first, it can invalidate the
feature.** If it survives, the QR wants its own full-screen view, and the
prototype is the right place for the camera and terminal/font checks.
