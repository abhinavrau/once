# Prototype: the one-time password moment

Status: open
Labels: wayfinder:prototype
Blocked by: 10 (resolved)

## Question

Ticket 06 fixed the mechanics: Once generates the restic repo password at first
backup, shows it exactly once, and the TUI makes the user type it back before
the form closes. The CLI prints it with a banner and cannot gate a scripted
run. What is still open is the screen itself, and the ticket that fixed the
mechanics said outright that this moment deserves a prototype rather than a
one-line prompt. It is the single moment that decides whether recovery is
possible at all.

Build the screen and react to it.

- **Layout and words.** What the user reads, in what order. The warning has to
  land without becoming a wall of red text the eye skips.
- **The capture affordances.** Copy to clipboard (precedent: `ctrl+p` on the
  Tailscale policy snippet), and the QR code if
  [ticket 10](10-research-terminal-qr-secret-handoff.md) says it is possible.
  Where they sit relative to the secret, and how they are labelled.
- **The type-it-back gate.** Whether the field is masked (masking a value that
  is on screen above it is theatre), what a mismatch says, and whether there is
  any escape hatch for a user who genuinely cannot proceed.
- **The CLI banner.** Same information, no gate, plain stdout.

Link the prototype from this ticket as an asset.

## Handoff from ticket 10

[The QR research](10-research-terminal-qr-secret-handoff.md) is resolved. It
found the terminal rendering to be cheap and correct, and the *payload* to be
the problem: no password manager ingests a raw-text QR, and neither Apple nor
Google documents a copy affordance for plain text, so a QR of the secret itself
rests on undocumented phone behaviour. A `https://` payload is the one thing
both platforms are documented to handle well.

**The QR therefore encodes a one-time URL, not the secret.** The research called
the URL a dead end on the grounds that `once-admin` publishes no host ports.
That is true of `once-admin` (admin.go:60-61) but not of Once: the Proxy
publishes 80 and 443 on the host (proxy.go:111-114), and the pinned
`basecamp/kamal-proxy:once-01` image accepts `--tls-certificate-path` and
`--tls-private-key-path` (verified against the real binary), so Once can serve
the page over TLS on the LAN with a cert it mints itself.

### One path for both frontends

**Decided in review, and it overrides ticket 06.** That ticket had the TUI
gating and the CLI printing - two behaviours for the single moment recovery
depends on. Instead there is **one password-handoff path**, driven by both
frontends, and it opens by asking the user how they want to take custody:

- **Show it on screen** - the secret is rendered where they are.
- **Send it to my phone** - a QR code of the one-time URL, per the design below.

The type-it-back gate follows either choice. The rendering differs per frontend
the way ADR-0012 already establishes for progress; the decision, the token, the
gate, and the teardown do not.

**Non-interactive runs keep the old behaviour as a fallback.** With no TTY,
`once backup enable` prints the password with the banner and no gate, so
scripted installs do not hang on a question nobody can answer.
`--password-delivery=screen|qr` chooses without asking.

**Tension to resolve, not to skip:** ADR-0013 states outright that there is no
CLI prompt, because the TUI is the interactive path and a second interactive
implementation would have to negotiate the terminal with `runShowingProgress`.
That reasoning was about *reading a secret in*, and this is about handing one
out, but the consequence it warned against - the CLI growing an interactive path
- is exactly what this decides to do. The prototype must either show the
terminal negotiation is tractable here, or the ADR gets an amendment saying why
this case differs. Do not leave it unstated.

### The design to prototype

One implementation, one branch. The token, the handler, and the QR renderer are
shared; only the URL and the cert differ.

- **Tailscale enabled:** encode `TailnetURL("once-admin")`, which Once already
  computes and which already carries a valid Tailscale cert. No cert to mint,
  no browser warning.
- **Tailscale disabled:** mint a self-signed cert with the LAN IP in the SAN
  list (`crypto/x509`, stdlib), `CopyToContainer` it into the Proxy the way the
  state file already gets there (proxy.go:261, no new mount), register a
  temporary service (`kamal-proxy deploy once-secret --host <lan-ip> --target
  <admin> --tls --tls-certificate-path … --tls-private-key-path …`), and remove
  it when the screen closes.
- **The page** puts the password in a form field, so the phone's password
  manager offers to save it. That is the entire reason for serving a page
  rather than encoding the secret.
- **The endpoint** is one handler on the daemon's admin server, which today has
  a single `/up` route (background/admin.go:68). The token must be
  **single-use** and expire in minutes: it travels in a URL, so it lands in the
  phone browser's history and possibly in browser sync.

### Decided, with the trade-off named

The self-signed path shows a full-page browser warning. **Accepted:** self-
hosters understand it, and the screen says to expect it before the user scans.
The cost, recorded here so it is not rediscovered later: that warning is also
the signal that would tell a user their connection had been intercepted, and
making it routine on this screen removes that signal. On a home LAN the
practical risk is low; a plain-text QR would have zero network exposure at all.

### Scope

**Both delivery options ship, gated on the phone test below.** If the test
fails, the QR option does not ship and the screen option carries the moment
alone; the choice prompt collapses to nothing to choose.

### What the prototype must settle first

- **The phone test, five minutes, and it now tests the right thing.** Does the
  password-manager save prompt still fire on an origin the user clicked a cert
  warning through? That behaviour is the whole rationale. If it does not fire,
  the URL puts the secret on the network and buys nothing over a plain-text QR,
  and the fallback is the screen itself: chunked rendering, a real "write this
  down" gate, and the type-it-back confirmation.
- **The wording that precedes the warning.** The user must know the warning is
  coming, and why, before they scan. Getting this wrong teaches exactly the
  habit self-hosting security depends on not having.
- **LAN IP discovery** must exclude `docker0` and `br-*` interfaces, or Once
  hands the phone a Docker bridge address that resolves to nothing. Multiple
  real interfaces need a rule, not a guess.
- **The password's alphabet** matters less now that the payload is a short URL
  rather than a 64-character secret (the research's 49x25-does-not-fit finding
  applied to the secret itself). Shorter is still kinder to anyone typing it
  back by hand.

### Do not

- **Do not wrap the secret in `otpauth://`** to force manager ingestion. The
  manager would treat it as a TOTP seed, derive rotating codes from it, and the
  original string would be unrecoverable from its UI.
- **Do not serve the page over plain HTTP.** `http` on a LAN IP is not a secure
  context, so browsers suppress the very save prompt this design exists to
  trigger.

## Resolution

(pending)
