# Prototype: the one-time password moment

Status: open
Assignee: Abhinav Rau
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

## Added by ticket 07

The custody flow now has a **fixed position in the enable sequence**, decided by
[ticket 07](07-backup-run-design.md):

    generate password -> run the validation probe -> custody flow -> Enabled = true

The probe needs the password (it `init`s the repos with it), so generation comes
first. But the custody ritual - show or QR, then type it back - must not be
demanded for a configuration that then fails its probe, so it comes after.

Two consequences for this prototype:

- The screen can assume **both repos are reachable and initialized** by the time
  it renders. A QR served over the tailnet or a LAN IP is being offered to a user
  whose configuration is already known-good, so nothing on this screen has to
  hedge about whether backups will work.
- A probe failure means this screen is **never reached**, and the user sees the
  restic error in the still-populated form instead. So there is no "we saved your
  password but backups are broken" state for this flow to render - except on
  retry, where the password already exists and must not be regenerated.

## Comments

Ticket 08 resolution reaches in: the custody screen is a **recovery kit**, not
just the password. It must also show the offsite repo URL and the backend env
var **names** (never the values - the user holds those at their provider).
Rationale: a user who dutifully saved only the password still cannot mount the
repo on a fresh host.

### Prototype built - awaiting reaction

**Asset: branch `prototype/password-moment`, `cmd/prototype-password-moment/`**
(commit 3f0dd44). Run it:

    git checkout prototype/password-moment
    go run ./cmd/prototype-password-moment            # the TUI flow
    go run ./cmd/prototype-password-moment --banner   # the no-TTY CLI banner
    go run ./cmd/prototype-password-moment --serve    # phone page only, prints URL

The flow: custody choice (screen / phone) -> reveal or QR -> type-back gate ->
done. The reveal screen has **three variants, left/right arrows switch**:

- **A Ceremony** - boxed card, one-line warning, password large and centered,
  kit lines muted below, ctrl+p copies the password.
- **B Recovery kit** - the whole screen is a double-bordered KIT document
  (password, repo, cred names, date, host); ctrl+p copies the entire block;
  framing is "save this document", not "save this string".
- **C Checklist** - numbered ritual (copy / store / prove), password inline in
  step 1; the primary affordance is the workflow.

The QR path serves the real page over HTTPS on the LAN IP with a self-signed
cert, so **the gating phone test can run against the actual artifact**: scan
with a stock camera, tap through the warning, tap "Save in password manager",
and see whether the save prompt fires (iPhone and Android). Verified working
end to end from this machine (page serves, POST burns the token, burned link
gets 410).

Decisions the prototype embodies, to react to:

- **Password**: 32 chars base32 (A-Z 2-7, 160 bits), shown grouped in 4s; the
  gate strips dashes/spaces and ignores case, so phone keyboards can't lose.
- **Gate field unmasked** - masking a value whose custody we just handed the
  user is theatre.
- **Escape hatch exists but is loud**: esc -> "skip without confirming?" -
  password stays persisted, Enabled stays false, finish later with
  `once backup enable` (matches ticket 07's retry state).
- **QR wording precedes the code on its own intro screen** - the cert-warning
  explanation cannot share the QR screen anyway (ticket 10's 24-row budget),
  and this guarantees it is read before scanning.
- **LAN IP rule**: the default route's source address (UDP dial to 8.8.8.8,
  no packet sent), which excludes docker0/br-*/veth by construction; offline
  fallback scans interfaces skipping those prefixes. No guessing among
  multiple real interfaces - the kernel's routing choice is the rule.
- **Single-use = burned on submit** (not on GET), 10-minute TTL. Open
  question for the real build: burn-on-GET is stricter but a phone browser
  preload could consume it.

**ADR-0013 tension resolved by demonstration, not amendment-dodging**: the CLI
already runs a full bubbletea program every time it shows progress
(cli_progress.go:58, `tea.NewProgram(p).Run()`), sequentially, handing the
terminal back on quit. The custody flow is just another such program run after
the probe's progress program exits - the prototype *is* that shape. No
concurrent terminal negotiation exists. ADR-0013 should get one amendment
line when this ships: reading a secret in stays TUI-only; handing one out
reuses the shared custody component under the same sequential-program pattern
the CLI's progress rendering already uses.

**Still needs the human**: (1) the five-minute phone test above - it decides
whether the QR option ships at all; (2) pick a reveal variant or name the
hybrid; (3) react to the gate wording and the skip hatch.
