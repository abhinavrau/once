# Terminal QR code for the restic repo password

Status: research note, 2026-08-13. Question: Once shows the restic repo
password exactly once. Can a terminal render a QR code a phone can actually
scan, so the user does not have to hand-type a 64-character secret? What should
it encode, and what does it cost?

Answers ticket `10-research-terminal-qr-secret-handoff`. Feeds the
`11-prototype-password-moment` prototype, which is blocked on this.

> **Partly superseded, 2026-08-14.** Its measurements stand and its warning about
> raw-text payloads stands. Its conclusion that the URL option is a dead end does
> **not**: that holds for `once-admin`, which publishes no host ports, but not for
> Once, because the Proxy publishes 80 and 443 (`proxy.go:111-114`) and the pinned
> kamal-proxy image accepts `--tls-certificate-path`/`--tls-private-key-path`.
> The design that shipped to `11-prototype-password-moment` encodes a one-time
> URL - the tailnet URL when Tailscale is on, a self-signed cert on the LAN IP
> when it is not - because a page can put the password in a form field, which is
> the only thing a password manager will save. The size analysis below therefore
> applies to a short URL, not to a 64-character secret.

## TL;DR

1. **Technically viable, but the payoff is in doubt.** The half-block technique
   produces a geometrically correct QR symbol and it decodes - verified with a
   real decoder (OpenCV `QRCodeDetector`), not by eye. The naive
   one-character-per-module render fails every time, exactly as the ticket
   suspected. The problem is not the terminal. It is the phone: see 7 below.
2. **If it is built, it must encode the password itself, as plain text.** The URL
   option is a dead end for the case that matters (see
   [What it should encode](#what-it-should-encode)): `once-admin` has no host
   ports and is only reachable over the tailnet, so a URL QR works only when
   Tailscale is enabled - i.e. exactly not on the fresh headless box where the
   password is first shown.
3. **The size floor is the binding constraint, and it is decided by the
   password's alphabet, not by the QR library.** Once's TUI hard-floors the
   terminal at 80x24 (`NewTerminalSizeGuard(80, 24)`,
   `internal/ui/app.go:135`). A 64-character *mixed-case* secret at error
   correction level Q renders 49x25 - one row too tall to fit at all. The same
   64 characters restricted to **uppercase + digits** render 41x21, because QR
   switches from byte mode to alphanumeric mode. That is the whole ballgame:
   pick the alphabet and you get level Q inside the minimum terminal with three
   rows to spare.
4. **The render must force its own colours and must not inherit the palette.**
   An inverted symbol is *spec-legal* (ISO 18004 clause 6.2 permits reflectance
   reversal and clause 12 tells decoders to try it), but ZXing does not try it
   unless the app opts in, and ZXing's own Android app does not opt in. So an
   inherited dark theme is a coin flip on the user's phone. Force both foreground
   and background, the way `qrencode -t ANSIUTF8` does.
5. **Cost is one dependency** - `github.com/piglig/go-qr`, MIT, maintained, and
   because its only requirement is the testify chain `once` already has, the real
   cost is +1 module and +2 `go.sum` lines. See [Library](#library).
6. **I could not scan it with a phone.** I have no camera. Everything here is
   either a spec requirement or a software decode against a simulated capture,
   so nothing below should be read as "it scans".
7. **Settle the phone question before writing any code.** No password manager -
   Bitwarden, 1Password, Apple Passwords, Google - ingests a raw-text QR; they
   scan `otpauth://` only, and import by file. Apple documents no copy affordance
   for a plain-text payload at all, and Android's docs say "view text", not copy.
   The one payload type phones handle well is a URL, and that is precisely the
   one Once cannot serve here. **So the feature's value rests on an undocumented
   behaviour.** A person with a phone can resolve it in five minutes; that test
   is worth more than anything else in this note. See
   [The catch](#the-catch-and-it-is-a-big-one).

## Is it scannable?

### The geometry problem, confirmed

Terminal cells are roughly 1:2 (width:height). One QR module per character cell
therefore produces modules stretched 1:2, which is outside anything the spec
tolerates. The half-block technique (`▀` U+2580, `▄` U+2584, `█` U+2588, space)
packs two module rows into one text row, so a module occupies
`cell_width x cell_height/2` pixels - square whenever the cell is 1:2, and close
enough otherwise.

I tested both, decoding with OpenCV:

| render | result |
| --- | --- |
| half-block, all seven cell metrics tested (7x15 through 10x24) | decodes, every payload, every EC level |
| naive one cell per module, same metrics | fails, every payload, every EC level |

The half-block render survived cell aspect ratios that are *not* exactly 1:2 -
8x17 (JetBrains Mono at a 1.2 line height) and 9x19 (Menlo 13px on macOS) both
decode, giving modules 6% and 5.5% off square. So the technique does not require
a terminal configured to an exact 1:2 cell, which was the main thing I expected
to bite.

### This is not a novel technique

Worth saying plainly before the measurements: the reference QR implementation
already ships it. `libqrencode` - Fukuchi's library, the one behind the
`qrencode` CLI - renders `-t UTF8` using exactly these glyphs:

```c
empty   = " ";
lowhalf = "\342\226\204";   /* U+2584 LOWER HALF BLOCK */
uphalf  = "\342\226\200";   /* U+2580 UPPER HALF BLOCK */
full    = "\342\226\210";   /* U+2588 FULL BLOCK */
```

and defaults its margin to 4 modules for standard QR (2 for Micro QR):

```c
if(margin < 0) {
    if(micro) { margin = 2; } else { margin = 4; }
}
```

So the character set and the quiet zone I tested are the same ones the reference
implementation chose, and `qrencode -t UTF8` output is scanned by people daily.

**Tailscale ships this exact feature**, which is the most directly relevant prior
art we have: `tailscale up --qr` / `--qr-format` prints a half-block QR to
stderr, wrapping `skip2/go-qrcode`. Two details from `util/qrcodes/qrcodes.go`
are worth copying:

- Its `auto` format detection on Linux checks `LC_CTYPE`/`LANG` end in `.UTF-8`,
  that the writer is a tty, that the console keyboard is in `K_UNICODE` mode, and
  then does an `ioctl(GIO_UNIMAP)` to confirm `█ ▀ ▄` **actually exist in the
  current console font**, falling back to ASCII otherwise. That is the correct
  paranoia for a tool that runs on bare consoles.
- Its source carries `const inverse = false // Modern scanners can read QR codes
  of any colour.` My measurements say that comment is optimistic - see
  [Polarity](#polarity).

Other shipping implementations and their margins, for calibration: python-qrcode
`print_ascii()` uses half blocks with `border=4` (and U+00A0, not U+0020, for the
light module - it survives whitespace stripping but can confuse width
calculations); `qrcode-terminal` ships a **1-module** quiet zone in both modes
due to an off-by-one in its `repeat()` helper, and relies purely on decoder
tolerance. Do not copy that one.

### What the spec requires, and whether the render satisfies it

**Quiet zone.** ISO/IEC 18004:2015 clause 6.3.8, verbatim:

> This is a region which shall be free of all other markings, surrounding the
> symbol on all four sides. Its nominal reflectance value shall be equal to that
> of the light modules. For QR Code symbols its width shall be 4X.

Note the second sentence: the quiet zone must match the **light modules**, not
"white". If you render inverted, the quiet zone must be dark. The renderer gets
this right automatically only if it derives the margin colour from the light
module colour rather than hard-coding a background.

**Polarity - and here the ticket's framing, and mine, was wrong.** The spec does
*not* require dark-on-light. Clause 6.2, "Reflectance reversal":

> Symbols are intended to be read when marked so that the image is either dark
> on light or **light on dark** [...] therefore in the case of symbols produced
> with reflectance reversal references to dark or light modules should be taken
> as references to light or dark modules respectively.

and the reference decode algorithm in clause 12 *requires* a decoder to try it:

> If no candidate areas are detected, reverse the colouring of the light and
> dark pixels and recommence at the beginning of step b.

So an inverted terminal QR is a **conforming symbol**. The problem is not the
spec, it is that real decoders do not all implement clause 12 - see
[Polarity](#polarity), where this turns out to matter a great deal anyway.

**Everything else:**

| requirement | source | what the half-block render gives | satisfied |
| --- | --- | --- | --- |
| Quiet zone 4X, all four sides, light-module colour | ISO/IEC 18004:2015 clause 6.3.8 | 4 modules, added by the renderer; counted in every size in this note | yes |
| Square modules | ISO/IEC 18004 symbol structure | square when the cell is 1:2; 5-6% off on common real cells, which decoded fine | yes |
| Reflectance contrast | ISO/IEC 18004 print-quality grading | an LCD is far past any print threshold; measured floor is Michelson 0.08 | yes |
| Error correction level | L 7% / M 15% / Q 25% / H 30% ([qrcode.com](https://www.qrcode.com/en/about/error_correction.html), ISO clause 5.1 f) | free choice; Q, and it is free - see below | yes |

ISO/IEC 18004 is paywalled; the clause text above was read from a copy of the
2015 edition. Denso Wave's public documentation agrees in plain language ("QR
Code requires a four-module wide margin at all sides of a symbol"). Where
possible I validated behaviourally against a decoder rather than trusting the
wording - the quiet-zone, contrast and polarity rows below are measurements.

### The thing I cannot test

I have no camera, so **I have not scanned this with a phone and I am not
claiming it scans.** What I did instead:

- Rendered the half-block output to a pixel canvas the way a terminal paints it
  (a grid of `cell_w x cell_h` cells, block glyphs tiling the cell exactly).
- Ran that through a crude camera model - area-resample to a target
  pixels-per-module, Gaussian defocus, additive sensor noise.
- Decoded with OpenCV 5.0.0's `cv2.QRCodeDetector`.

That is strong evidence the symbol is *correct* and that the geometry survives
capture. It is not evidence that a specific phone's scanner UI will offer to
copy the text, or that a given terminal font paints block glyphs without seams.
Both need a human with a phone. Put that in the prototype.

## Empirical results

All numbers below are from a scratch Go program (`skip2/go-qrcode` to encode,
half-block renderer) plus a Python rasterize-and-decode harness (OpenCV 5.0.0).
Neither is checked in; nothing in this repo was modified except this file.

### Sanity check: the encoder agrees with the spec

Before trusting any size number, I swept payload length and compared the
version the library picks against the ISO/IEC 18004 byte-mode capacity table.
Exact match at all four EC levels for versions 1-8 (e.g. first length needing
version 5 at level M is 63, so version 4-M holds 62 - the table says 62). The
library's version selection is correct.

### Payload size and render size

Render size in **columns x rows**, half-block, including the 4-module quiet zone.
Once's floor is 80x24.

64-character secret:

| alphabet | mode | L | M | Q | H |
| --- | --- | --- | --- | --- | --- |
| base62 / base64 (mixed case) | byte | 41x21 | 45x23 | **49x25 does not fit** | 53x27 does not fit |
| uppercase base32 (A-Z 2-7) | alphanumeric | 37x19 | 41x21 | **41x21** | 45x23 |

The ~60-character URL behaves like the mixed-case password (63 chars, byte mode:
45x23 at M, 49x25 at Q) because URLs contain lowercase. Uppercasing the
scheme and host - legal, they are case-insensitive - and dropping the query
string buys back a version:
`HTTPS://ONCE-ADMIN.TAIL1A2B3C.TS.NET/BACKUP/9F3AC1E2` (52 chars) is 41x21 at
level Q, the same as the base32 password.

Full fit table against the 80x24 floor, spare rows after the QR:

| alphabet | level | 64 ch | 52 ch | 44 ch | 32 ch |
| --- | --- | --- | --- | --- | --- |
| base62 | M | 45x23 (1) | 41x21 (3) | 41x21 (3) | 37x19 (5) |
| base62 | Q | **49x25 (none)** | 45x23 (1) | 41x21 (3) | 37x19 (5) |
| base32 | M | 41x21 (3) | 37x19 (5) | 37x19 (5) | 33x17 (7) |
| base32 | Q | **41x21 (3)** | 41x21 (3) | 37x19 (5) | 37x19 (5) |
| base32 | H | 45x23 (1) | 45x23 (1) | 41x21 (3) | 37x19 (5) |

A 64-character uppercase base32 secret is 320 bits of entropy, which is far more
than restic needs, and it lands at 41x21 at level Q. There is no reason to
prefer a mixed-case alphabet here: it costs a QR version, buys ~0.95 bits per
character, and makes the secret harder to read aloud or type as a fallback.

**On error correction level.** The usual advice for screen QR is "use M, never
H": ECC protects against print damage and dirt, which do not exist on an LCD,
while the failure modes that *do* exist here are geometric (seams, aspect drift,
truncation) and ECC does nothing for those. Raising the level grows the symbol,
and in a height-limited terminal that directly eats the row budget. Denso Wave
concurs that M is the usual choice, and ISO Annex K.2 calls M the "Standard"
level.

That advice is right, and it happens not to bind here: with the base32 alphabet,
**level Q and level M produce the same 33x33 symbol** (both encoders agree), so Q
is free. Take it. If the alphabet decision goes the other way and Q starts
costing a version, drop to M without hesitation - and do not go to H under any
circumstances, which costs two versions and buys protection against a threat this
medium does not have.

### Capture resolution

How few captured pixels per module still decode (ideal image, then with
defocus and noise):

| px per module | clean | +defocus | +defocus +noise |
| --- | --- | --- | --- |
| 8 | pass | pass | pass |
| 6 | pass | pass | pass |
| 4 | pass | pass | pass |
| 3 | pass | pass | marginal - level M passed, level Q failed at heavy blur |
| 2 | pass | fail | fail |
| 1.5 | fail | fail | fail |

End to end, the recommended render (64-char base32, level Q, 8x16 cells, so the
terminal paints modules at 8x8 px):

| simulated capture | px/module | result |
| --- | --- | --- |
| close, steady | 12 | pass |
| normal, ~30 cm | 8 | pass |
| further, ~50 cm | 4.8 | pass |
| far, ~80 cm | 3 | pass |
| far and shaky | 3 | fail |
| very far, ~1.2 m | 2 | fail |

On a typical laptop screen a 41-module-wide code at an 8px cell is about 33 mm
across; a phone held at normal reading distance is nowhere near the floor. The
resolution budget is comfortable. This is the constraint people worry about and
it turns out not to be the problem - the terminal row budget is.

### Quiet zone

Trimming the quiet zone below the spec's 4 modules, with a dark border pressed
directly against the symbol (the case a TUI actually creates):

| quiet zone | result |
| --- | --- |
| 4 modules | pass |
| 3 modules | pass |
| 2 modules | pass |
| 1 module | pass |
| 0 modules | fail |

OpenCV tolerates a thinner zone than the spec demands. So does ZXing - its QR
path has **no quiet zone validation at all** (the only `quiet` hits in the Java
core are the 1D readers and `QRCodeWriter.QUIET_ZONE_SIZE = 4` on the *encode*
side), and its detector deliberately gives a symbol touching the image edge the
benefit of the doubt.

There is still no reason to lean on that: the four rows and columns are already
counted in the 41x21 figure and there is nothing else to spend them on. **Keep
all 4, and do not draw a border or caption inside them.** Also keep them the
colour of the *light modules*, per clause 6.3.8 - a dark-rendered code needs a
dark quiet zone, not a white one.

### Polarity

Once's palette is theme-derived and can be dark (`palette.go` synthesizes from
detected terminal colors; `isDark` is set from the detected background
lightness). If the QR inherits that, dark modules get painted in the *light*
foreground color:

| polarity | result |
| --- | --- |
| dark modules dark, quiet zone light (`#1a1b26` on `#c0caf5`) | pass |
| inherited dark theme - dark modules light, quiet zone dark | **fail** |
| dark modules dark on white | pass |
| inverted on white | **fail** |

OpenCV does not attempt an inverted scan. Neither does ZXing by default - it has
the capability but only behind an **opt-in hint**:

```java
/**
 * If true, also tries to decode as inverted image. All configured
 * decoders are simply called a second time with an inverted image.
 */
ALSO_INVERTED(Void.class),
```

Decoder support is genuinely split:

| decoder | inverted QR | evidence |
| --- | --- | --- |
| ZXing (Java) | opt-in, **off by default** | `ALSO_INVERTED` hint; `MultiFormatReader.decodeInternal` only flips when the hint is present |
| ZXing's own Android app | **does not enable it** | `DecodeThread` sets only `POSSIBLE_FORMATS`, `CHARACTER_SET`, `NEED_RESULT_POINT_CALLBACK` |
| zxing-cpp | **on by default** | `ReaderOptions.h`: "Try detecting inverted ... codes (default: true)" |
| OpenCV | no | measured above |
| Apple Vision | undocumented | `VNDetectBarcodesRequest` docs never mention invert/reverse/polarity |
| ML Kit | undocumented | silent on polarity; third-party wrappers add their own `checkInverted` that manually inverts the frame, which would be pointless if ML Kit did it |

So ISO clause 12 says a conforming decoder must try inversion, and the single
most widely deployed decoder library does not do it unless the app opts in.
Relying on it is a coin flip on the user's phone.

This is not theoretical. `qrcp` issue #150, "QR code is unscannable", resolved as
exactly this: "Your theme is inverting the qr code. Can you try it again with a
theme with black background?" / "Yes, the QR code is perfect on a dark theme."

**The render must set both foreground and background explicitly on every cell of
the block, including the quiet zone, and must ignore the palette.** That is what
`qrencode -t ANSIUTF8` does (`\033[40;37;1m` per line) and what python-qrcode's
`print_ascii(tty=True)` does (`\x1b[48;5;232m` / `\x1b[38;5;255m`). Forcing the
colours removes the polarity question entirely rather than betting on the
scanner.

This has a consequence worth flagging. Half-block glyphs need *both* colors -
`▀` paints foreground on the top half and background on the bottom half - so the
QR cannot be drawn with foreground-only styling. And `internal/ui/app.go:302-305`
only forces `colorprofile.TrueColor` when the terminal supports it; under a
degraded profile (`NO_COLOR`, a dumb terminal, a non-color CI pty) color
attributes get stripped and the code renders in the terminal's default
foreground and background. On a dark theme that is inverted, i.e. unscannable.
**Detect that case and suppress the QR rather than draw an unscannable one**, or
the feature silently fails for the user who most needs it.

### Contrast floor

Michelson contrast down to 0.08 still decoded (fg luma 120 vs bg 140). Contrast
is not a real constraint on a screen; any sane color pair clears it by an order
of magnitude. Noted only to close the question.

### Terminals that mangle half-blocks

The failure mode is a terminal or font that does not tile block glyphs to the
full cell, leaving an unpainted seam at the bottom of each text row - which
lands on every second QR module row.

| seam | pixel-exact | through a camera (6 px/module, mild defocus) |
| --- | --- | --- |
| 0 px | pass | pass |
| 1 px | fail | pass |
| 2 px | fail | fail |
| 3+ px | fail | fail |

A 1px seam is rescued by the camera's own resampling - it averages away below
the module. 2px or more is fatal.

A second, independent sweep across seam width *and* capture distance found the
same budget and one counterintuitive extra:

| seam \ camera px/module | 1.5 | 2 | 3 | 4 | 6 | 8 |
| --- | --- | --- | --- | --- | --- | --- |
| 0 px | fail | pass | pass | pass | pass | pass |
| 1 px (line-height ~1.07) | fail | pass | pass | pass | pass | **fail** |
| 2 px (~1.14) | fail | pass | pass | pass | **fail** | fail |
| 3 px (~1.23) | fail | pass | fail | fail | fail | fail |
| 4 px (~1.33) | fail | fail | fail | fail | fail | fail |

Three consequences:

1. **Moving the phone closer makes it worse.** Below ~4 px/module the lens blur
   smears the seam away; above that the camera *resolves* the seam and the decode
   fails. There is a window, not a threshold. This is the mechanism behind "it
   won't scan no matter how close I hold it".
2. **On a pixel-perfect screenshot, any seam at all is fatal**, because a thin
   light line between module rows breaks the finder pattern's 1:1:3:1:1 run
   ratios outright. Screenshot-and-decode is a *harsher* test than photographing
   the screen, which is worth knowing if we ever write an automated check.
3. **The budget is about 2px on a 16px cell, i.e. line-height under ~1.15.** At
   1.33 nothing decodes at any distance.

That last point reframes the risk. It is not mainly "bad fonts"; it is
**line-height configuration**, which is a setting many users have changed. The
bug reports are consistent on this: Alacritty #5485 (the canonical writeup),
WezTerm #584 (cause is `line_height != 1.0`), iTerm2 #11577 ("SF Mono with 110%
line-spacing ... now there are gaps"), foot #1430 (same, in a terminal that draws
its own glyphs), and Windows Terminal #2779 (gaps at 125/150/175/225% display
scaling, clean at 100% and 200%). Most on point, Zed #17904 is *literally this
feature failing*: "Expo QR code not display well in terminal ... so qrcode is not
scannable", with the maintainer answering "By default Zed uses a golden ratio
terminal line height ('comfortable'), you can switch this to 'standard' ... and
you won't have margin between lines".

The mitigating news is that every major terminal now synthesizes block elements
itself, snapped to the cell, ignoring the font - kitty (always), VTE/GNOME
Terminal (always), Ghostty (always), iTerm2 (always), WezTerm
(`custom_block_glyphs`, default true), Alacritty >= 0.10
(`font.builtin_box_drawing`, default true), Windows Terminal >= 1.21
(`builtinGlyphs`, default true), foot (draws its own unless told otherwise). So
the default configuration is safe nearly everywhere, and the residual risk is
concentrated in users who raised their line height.

### Half-blocks are East Asian Width "Ambiguous"

This one I missed on the first pass and it is a genuine hazard. From
`EastAsianWidth-17.0.0.txt`:

```
2580..258F     ; A  # So    [16] UPPER HALF BLOCK..LEFT ONE EIGHTH BLOCK
2590..2591     ; N  # So     [2] RIGHT HALF BLOCK..LIGHT SHADE
```

All three glyphs we need (U+2580, U+2584, U+2588) are **Ambiguous**, not Narrow -
and the range is not even internally consistent, since U+2590 RIGHT HALF BLOCK is
Narrow. UAX #11 says ambiguous characters are wide in East Asian legacy contexts
and narrow otherwise, and "if the context cannot be established reliably, they
should be treated as narrow characters by default."

One cell is therefore a *default*, not a guarantee. Under a CJK locale, or with
the "ambiguous width = double" setting that kitty, WezTerm, Windows Terminal and
iTerm2 all expose, every glyph becomes two cells wide - which doubles the width,
halves the effective aspect ratio, and produces exactly the 1:2 stretched code
that never decodes. It would also silently overflow the 80-column budget.

Once already depends on `mattn/go-runewidth` and `clipperhouse/displaywidth`, so
the render can check the width it will actually get and refuse rather than draw a
broken code. Tailscale's `GIO_UNIMAP` font probe is the thorough version of the
same idea.

## What it should encode

### The URL option is a dead end

`internal/docker/admin.go` creates `once-admin` with **no host ports** - it is
an nginx container bind-mounting the daemon's Unix socket
(`adminContainerSocket = "/var/run/once-admin.sock"`), on the once network, and
it is published only by Docktail as a Tailscale Service. So:

- A phone can reach it **only over the tailnet, and only when Tailscale is
  enabled.** There is no localhost path (a phone cannot reach the server's
  localhost) and no LAN path (no published port).
- The password moment happens when backups are **configured** (ticket 06 moved
  it there from first-backup, so a human is actually present). That is early,
  and on a fresh headless box Tailscale is very often not enabled yet - the
  exact case where "I can't capture this secret" is fatal is the case with no
  URL to point at.
- `Admin.RequireDaemon` also means the whole path depends on the background
  daemon having published its socket.

It also makes the handoff worse where it does work: the secret would now exist
in an nginx access path and be fetched over the network, versus a QR that is
just a re-rendering of characters already on the screen.

Dead end. Do not build it.

### Encode the password, as plain text

- No server, no network, no Tailscale dependency. Works on a headless box with
  no connectivity, which is the case that matters.
- **Adds no new exposure**: the secret is already rendered on the same screen in
  the same scrollback. A QR of it is the same bytes in a different font. The
  threat model does not change. (Shoulder-surfing changes slightly - a QR is
  capturable from further away than 64 characters of text are readable - but
  anyone who can photograph the QR can photograph the text.)

### The catch, and it is a big one

The technical side works. The **phone side is much weaker than the ticket
assumed**, and this is the finding that should drive the decision.

Only three payload types are acted on natively by stock phone scanners:
`https://`, `otpauth://`, and `WIFI:`. A raw text payload is not one of them.

What the vendors actually document:

- **iOS Camera / Code Scanner.** Every Apple description of the result is "tap
  the **link**". Apple documents **no** behaviour and **no** copy affordance for
  a plain-text payload, anywhere. That is absence of documentation rather than a
  documented failure - but it is the whole of the first-party evidence.
- **Live Text** can copy, but it OCRs the visible characters; it does not read
  the QR payload. For a high-entropy secret that is a liability, not a feature
  (`l`/`1`/`I`, `0`/`O`, case).
- **Android.** The only official acknowledgement that plain text surfaces at all
  is Camera from Google: you can "open a browser, **view text**, or open apps".
  It says *view*, not copy. ML Kit's parsed-type list (URL, contact, calendar,
  email, phone, SMS, ISBN, WiFi, geo, AAMVA) **excludes plain text** - it comes
  back only as `rawValue`, for the app to do something with.
- **Bitwarden mobile:** the scanner exists only inside "Edit item -> Set Up TOTP
  -> Scan the QR code". `otpauth://` only. Import is file-only.
- **1Password mobile:** TOTP only for item data. Its Emergency Kit QR is not a
  plaintext secret dump - it carries partial key material and requires a
  confirmation code, and it is scanned by 1Password itself.
- **Apple Passwords (iOS 18+):** import is CSV/ZIP only; QR is for verification
  codes only.
- **Google Password Manager:** `.csv` only, no QR path in any Google doc.

So the optimistic flow - "scan it straight into your password manager" - **does
not exist on any of the four major managers.** The realistic best case is
scan -> the phone shows the text -> copy -> paste, and the *copy* step is exactly
the one nobody documents.

### What that means for the decision

The obvious generic advice at this point is "encode a URL instead", and for most
tools that would be right. **It is not available to us**, for the architectural
reason in the previous section: there is no URL a phone can reach at the moment
the password is shown. That is the whole difficulty of this particular problem -
the one payload phones handle well is the one Once cannot serve.

That leaves three honest options:

1. **Encode the raw password anyway.** Cheap, offline, correct, and it decodes.
   Worth it *if* a stock phone lets the user copy the text. Unknown.
2. **Do not build it**, and put the effort into the password screen itself -
   grouped/chunked rendering, a clear "write this down" gate, and the
   type-it-back confirmation ticket 06 already specifies.
3. **Build it, but be honest about what it is**: a way to get the secret onto the
   phone's screen so it can be read or copied, not a password-manager import.

I would not pick between these on the evidence I have. **One person with a phone
and five minutes can settle it**: render a plain-text QR, scan it with a stock
iPhone and a stock Android, and see whether you can get the string onto the
clipboard. If yes, option 1 is clearly worth the ~1 day it costs. If no, option 2
is the better use of the same day. Do that test before writing a renderer.

Do **not** work around it by wrapping the password in an `otpauth://` URI to make
managers ingest it. It would be a lie about the secret's type, the manager would
treat it as a TOTP seed and derive rotating codes from it, and the original
string - the thing restic actually needs - would be unrecoverable from the
manager's UI.

## Library

Nothing in the module graph provides a QR encoder. I checked `go.mod` and
`go.sum`: no QR, barcode, or imaging encoder of any kind.

### Recommendation: `github.com/piglig/go-qr`

| property | value |
| --- | --- |
| module | `github.com/piglig/go-qr` |
| version | `v1.1.0` (tagged 2026-05-29), commits through 2026-08-13 |
| license | MIT - `Copyright (c) 2023 piglig` |
| go.mod | `go 1.21`; requires only `stretchr/testify` (test-only), pulling `go-spew`, `go-difflib`, `yaml.v3` |
| cgo | none; pure Go, builds with `CGO_ENABLED=0` |
| cost to `once` | **+1 module, +2 `go.sum` lines** - the whole testify chain is already required by `once` at equal or higher versions |
| binary size | ~+236 KiB stripped |

API is exactly the shape a terminal renderer wants - no PNG, no image decode:

```go
func EncodeText(text string, ecl Ecc) (*QrCode, error)  // encode.go:7
func (q *QrCode) Size() int                             // qr_code.go:93
func (q *QrCode) Module(x, y int) bool                  // qr_code.go:98
```

EC levels are `Low`/`Medium`/`Quartile`/`High` (= L/M/Q/H). It does **not**
include a quiet zone in the module grid, so the renderer adds 4 modules of
margin itself - which is fine, and arguably better, because the renderer needs
to own the surrounding cells anyway to force their background color.

I verified this end to end: encoded with piglig, added a 4-module quiet zone,
rendered half-blocks, and decoded with OpenCV. Passes at every cell metric and
through the camera model, identical to the results tabulated above. Inverted
polarity fails, same as before.

### Cross-check

`piglig/go-qr` and `skip2/go-qrcode` agree on the chosen QR version for every
payload and EC level I tested, and both match the ISO/IEC 18004 byte-mode
capacity table exactly. The size numbers in this note are therefore not an
artifact of one library's version-selection logic.

### Why not the obvious alternatives

- **`github.com/skip2/go-qrcode`** - the one most people reach for. MIT, zero
  deps, has `Bitmap() [][]bool` (quiet zone included) and even a ready-made
  half-block renderer, `ToSmallString(inverseColor bool)`. But it is
  **unmaintained**: no tagged release ever, last commit 2020-06-17, 10 open PRs
  none merged in six years. It also carries a latent bug - in `encode()`
  (`qrcode.go:391-425`) the local `penalty` resets to 0 on every call while
  `q.symbol` persists, so the guard `q.symbol == nil || p < penalty` is never
  true on a *second* `encode()`. Since `Bitmap()` calls `encode()` each time,
  any change to `DisableBorder` after the first `Bitmap()` is silently ignored,
  and every call re-runs all 8 mask evaluations and throws the result away.
  Harmless if you set everything up front and call once, but it is a live bug in
  a package nobody is fixing. Worth noting that **Tailscale uses this library**
  for `tailscale up --qr`, which is a meaningful vote of confidence; the bug does
  not bite their usage either.
- **`rsc.io/qr`** - smallest by far (~+68 KiB), BSD-3-Clause, `Black(x, y) bool`
  is the right shape. Frozen since 2018, and the repo ships a cgo `libqrencode`
  sibling package (not imported, but a smell).
- **`github.com/boombuler/barcode`** - maintained, MIT, but exposes the symbol
  only as `At(x, y) color.Color`. Works, wrong abstraction.
- **`github.com/yeqown/go-qrcode/v2`** - 3 modules, ~+436 KiB, and the matrix is
  reachable only by implementing a `Writer`. Its `writer/terminal` submodule
  pulls in `nsf/termbox-go`, which would fight bubbletea.
- **`github.com/mdp/qrterminal/v3`** - does the entire job in one call
  (`HalfBlocks: true`, `QuietZone: 4`). Tempting, but it costs +3 modules, writes
  to an `io.Writer` rather than returning a string, and its default
  `BlackChar`/`WhiteChar` are raw ANSI escapes that would fight lipgloss
  styling. It also renders the final quiet-zone row as a half-height `▀` stripe
  whenever the padded size is odd - which it always is, since QR symbol sizes are
  always odd. Given the polarity requirement we need per-cell color control
  anyway, so owning the ~25-line renderer is both cheaper and better.

The renderer we write is small: walk `Module(x, y)` two rows at a time, emit
`█`/`▀`/`▄`/space, set foreground and background explicitly on every cell
including the quiet zone, and pad to an even row count so the bottom margin is a
full row.

## Cost

- One dependency: `github.com/piglig/go-qr`, MIT, pure Go, +1 module and +2
  `go.sum` lines.
- A small renderer in `internal/ui` (~25 lines) - walk `Module(x, y)` two rows at
  a time, emit half-blocks with forced foreground/background, add the 4-module
  quiet zone, pad to an even row count, suppress under a degraded color profile.
- A constraint on password generation: **use an uppercase alphanumeric
  alphabet** (base32, A-Z + 2-7). This is the load-bearing decision and it is
  cheap only if made *before* the password format is fixed. Retrofitting it
  later means either changing the format for existing installs or accepting a QR
  that does not fit an 80x24 terminal.
- A screen-layout constraint: at the 80x24 floor the QR needs 21 of the 24 rows.
  It does not co-exist with the password text, a heading, and the type-it-back
  confirmation on one screen. It wants to be **its own view** - the password
  screen offers "show QR", the QR view is the whole screen, escape returns.
- A width guard: half-blocks are EAW=Ambiguous, so check the width the terminal
  will actually give them and refuse rather than draw a stretched code.
- Roughly a day of work, plus the phone test.

The phone test is not part of the cost, it is the gate. It comes first.

## Scope check - what would make this not worth doing

Things I looked for that turned out **not** to be blockers:

- **Capture resolution.** Comfortable margin - roughly 6 camera px per module for
  a phone at 30 cm, against a floor of 2. Moving closer is the wrong instinct
  when a code will not scan; see the seam window.
- **Contrast.** Not a constraint on a screen. Michelson 0.08 still decoded.
- **Non-1:2 terminal cells.** Tolerated; 5-6% off square still decodes.
- **Quiet zone in a cramped TUI.** Decoders tolerate far less than the spec's 4
  modules, and we can afford all 4 anyway.
- **The half-block technique being exotic.** It is not. libqrencode, python-qrcode
  and `tailscale up --qr` all ship it.

Things that **are** real:

- **The phone side.** The largest risk, and the reason to gate. No password
  manager ingests a raw-text QR; Apple documents no copy affordance for one;
  Android documents "view", not copy. The payload phones actually handle well is
  a URL, which Once cannot serve at the moment that matters. Settle this with a
  real phone before building.
- **The 80x24 row budget.** Binding. It forces both the alphabet choice and a
  dedicated full-screen view. If the password format is already fixed as
  mixed-case base64 and cannot change, a level-Q QR does not fit the minimum
  supported terminal at all and you drop to level M at 45x23 - one spare row, no
  caption.
- **Line height, not fonts.** The seam failure is dominated by users who raised
  their terminal line height; over ~1.15 the code stops decoding at any distance,
  and the golden-ratio default in at least one editor terminal is already
  documented breaking QR codes. Terminal defaults are mostly safe because every
  major terminal now draws block elements itself.
- **EAW=Ambiguous half-blocks.** A CJK locale or an ambiguous-width=wide setting
  doubles the glyph width and guarantees an unscannable, over-wide code. Needs a
  guard.
- **Polarity under a degraded colour profile.** Must be detected and the QR
  suppressed, or the user gets an unscannable code with no indication.

Verdict: **nothing here is technically disqualifying, and the engineering is
cheap and well understood.** But the feature's value - not its feasibility -
rests on one undocumented phone behaviour. Do that test first. If it passes,
build it, with the base32 alphabet and its own view. If it fails, spend the day
on the password screen itself instead.

## Sources

### Standard

- ISO/IEC 18004:2015, read directly: clause 6.2 (reflectance reversal permitted),
  6.3.8 (quiet zone 4X, nominal reflectance equal to the light modules), 9.1
  (module width is the application's choice), clause 12 step b)5 (reference
  decoder must retry inverted), Annex K.2 (level M is the "Standard" level). In
  the 2024 4th edition the quiet-zone clause renumbers to 5.3.8.
- ISO/IEC 18004 byte-mode capacity table (Table 7) - used to validate the
  encoders' version selection; matched exactly for versions 1-8 at all four EC
  levels.
- <https://www.qrcode.com/en/howto/code.html> - Denso Wave (the format's
  inventor) on the four-module margin.
- <https://www.qrcode.com/en/about/error_correction.html> - EC levels and the
  7/15/25/30% recovery figures, plus "typically, Level M is most frequently
  selected".
- <https://www.unicode.org/Public/UCD/latest/ucd/EastAsianWidth.txt> and
  <https://www.unicode.org/reports/tr11/> - U+2580..U+258F are EAW=Ambiguous;
  narrow only by default, not by guarantee.

### Decoders

- <https://raw.githubusercontent.com/zxing/zxing/master/core/src/main/java/com/google/zxing/DecodeHintType.java>
  - `ALSO_INVERTED` is opt-in, not default. ZXing's own Android app
  (`DecodeThread`) does not set it. Its QR path has no quiet-zone validation.
- <https://github.com/zxing-cpp/zxing-cpp/blob/master/core/src/ReaderOptions.h> -
  zxing-cpp tries inverted by default, unlike the Java original.
- <https://developers.google.com/ml-kit/vision/barcode-scanning> - ML Kit's
  "2 pixels wide and 2 pixels tall" minimum per module, and the parsed-type list
  that excludes plain text.
- OpenCV 5.0.0 `cv2.QRCodeDetector` - the decode oracle for every PASS/FAIL here.
- <https://github.com/piglig/go-qr> and `github.com/piglig/go-qr@v1.1.0` source
  read from the module cache: `encode.go:7` (`EncodeText`), `qr_code.go:93`
  (`Size`), `qr_code.go:98` (`Module`), `LICENSE` (MIT, piglig 2023), `go.mod`
  (testify only).
- `github.com/skip2/go-qrcode@v0.0.0-20200617195104-da1b6568686e` source, read
  from the module cache: `qrcode.go:140` (`DisableBorder`), `qrcode.go:266`
  (`Bitmap()`), `qrcode.go:391-425` (the `penalty`/`q.symbol` bug),
  `qrcode.go:573` (`ToSmallString`), `version.go:3036`
  (`quietZoneSize() { return 4 }`), `LICENSE` (MIT, Tom Harwood 2014), `go.mod`
  (no requires). Used as the second, independent encoder for the cross-check.
- <https://github.com/rsc/qr>, <https://github.com/boombuler/barcode>,
  <https://github.com/yeqown/go-qrcode>, <https://github.com/mdp/qrterminal> -
  the rejected alternatives.

### Terminal rendering prior art

- <https://github.com/fukuchi/libqrencode/blob/master/qrenc.c> - the reference
  implementation's `-t UTF8` half-block glyph set (U+2580/U+2584/U+2588/space)
  and its 4-module default margin (2 for Micro QR), uniform across image and text
  modes. `-t ANSIUTF8` adds `\033[40;37;1m` to force colours. Plain `-t UTF8`
  paints the *light* modules, so it only scans on a dark background; `UTF8i`
  swaps them.
- <https://github.com/tailscale/tailscale/blob/main/util/qrcodes/qrcodes.go> and
  `cmd/tailscale/cli/up.go` - `tailscale up --qr`, half blocks via
  `skip2/go-qrcode`, with a `LC_CTYPE`/tty/`K_UNICODE`/`GIO_UNIMAP` probe to
  confirm the console font actually has the glyphs.
- python-qrcode `print_ascii()` (half blocks, `border=4`, U+00A0 for the light
  module) and `qrcode-terminal` (1-module quiet zone, off-by-one in its
  `repeat()` helper).

### Terminal rendering pitfalls

- <https://github.com/alacritty/alacritty/issues/5485> - the canonical writeup of
  block-glyph seams, with a repro.
- <https://github.com/wezterm/wezterm/issues/584>,
  <https://gitlab.com/gnachman/iterm2/-/work_items/11577>,
  <https://codeberg.org/dnkl/foot/issues/1430> - all trace to `line_height != 1.0`.
- <https://github.com/microsoft/terminal/issues/2779> - gaps at 125/150/175/225%
  display scaling, clean at 100% and 200%.
- <https://github.com/zed-industries/zed/issues/17904> - this exact feature
  failing: "qrcode is not scannable", caused by a golden-ratio default line
  height.
- <https://github.com/claudiodangelis/qrcp/issues/150> - "QR code is unscannable",
  root cause was the terminal theme inverting it.
- Built-in block-glyph drawing, now on by default nearly everywhere: kitty and
  VTE/GNOME Terminal and Ghostty and iTerm2 (always), WezTerm
  `custom_block_glyphs`, Alacritty `font.builtin_box_drawing`, Windows Terminal
  `builtinGlyphs`.

### Once's own code

- `internal/ui/app.go:135` (`NewTerminalSizeGuard(80, 24)`),
  `internal/ui/app.go:302-305` (truecolor only when detected),
  `internal/ui/terminal_size_guard.go` (the 80x24 floor is enforced, not
  advisory).
- `internal/ui/palette.go` - theme-derived palette, `isDark` from detected
  background lightness; the reason polarity must be forced.
- `internal/docker/admin.go` - `once-admin` has no host ports, bind-mounts the
  daemon Unix socket, and is exposed only as a Tailscale Service; the reason the
  URL option is a dead end.

### Phone-side ingestion

- <https://support.apple.com/en-us/102680> and
  <https://support.apple.com/guide/iphone/scan-a-qr-code-iphe8bda8762/ios> -
  Apple's QR scanning docs. Cover URL and vCard payloads and the
  notification-tap flow; **never** state that plain text can be copied.
- <https://support.google.com/camerafromgoogle/answer/12033278> - "open a
  browser, view text, or open apps". *View*, not copy.
- <https://bitwarden.com/help/integrated-authenticator/> - Bitwarden's scanner
  exists only under "Set Up TOTP"; <https://bitwarden.com/help/import-data/> -
  import is file-only.
- <https://support.1password.com/one-time-passwords/> and
  <https://support.1password.com/qr-code-security/> - TOTP only for item data;
  the Emergency Kit QR carries partial key material, not a plaintext dump.
- <https://support.apple.com/guide/iphone/import-passwords-iph984b8ac1f/ios> -
  Apple Passwords imports CSV/ZIP only.
- <https://support.google.com/chrome/answer/13068232> - Google Password Manager
  imports `.csv` only.
- <https://github.com/zxing/zxing/wiki/Barcode-Contents> and
  <https://github.com/google/google-authenticator/wiki/Key-Uri-Format> - the
  payload types phones actually act on: `https://`, `otpauth://`, `WIFI:`.

### Tickets

- `.scratch/vaultwarden-3-2-1-backup/issues/10-research-terminal-qr-secret-handoff.md`
  - the originating ticket.
- `.scratch/vaultwarden-3-2-1-backup/issues/06-backup-configuration-ux.md:186-189`
  - the password is generated at configure time, not at first backup.
