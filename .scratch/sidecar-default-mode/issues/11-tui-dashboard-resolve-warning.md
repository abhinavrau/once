# 11 - Flag an unresolvable Tailnet URL on the TUI dashboard

**What to build:** The dashboard panel's "Tailnet URL" row prints a URL computed
from the stored Magic DNS suffix - it is deterministic and never looked up, so
the row looks identical whether the name resolves or not. Tickets 09 and 10 gave
the CLI a warn-only note for this; the TUI is the last surface where a dead URL
still displays unqualified.

The row gains a marker when the hostname does not resolve from the machine
running the TUI, carrying the same conditional meaning as the CLI warning: the
service may still be registering; or, if this machine is on the tailnet, its
MagicDNS may be broken; other tailnet devices may be unaffected. The full
sentence does not fit a label/value row, so choose a presentation that fits the
panel - a short suffix or muted marker on the row itself, with the explanation
somewhere it can be read in full.

Unlike the CLI, rendering here must not resolve anything. The dashboard
re-renders on a one-second tick and the panel's View is pure, so a synchronous
lookup would stall the UI every frame. The resolution result must be fetched
asynchronously and cached on the model, with the view reading only the cached
verdict. The existing 5-second `tailscaleRefreshMsg` loop that already refreshes
Tailscale info is the seam to hang it on - do not introduce a second timer.

Until the first check has completed, the row shows no marker: an unknown verdict
must never render as a warning.

**Blocked by:** 10 - it extracts the URL-and-resolver core out from behind the
app-shaped gate, which this ticket calls from the async refresh.

**Status:** ready-for-agent

- [ ] A panel whose tailnet hostname does not resolve locally shows a marker on the Tailnet URL row, with the conditional explanation readable in full somewhere in the UI
- [ ] The panel's View performs no DNS lookup and no blocking work; the verdict is cached on the model and refreshed asynchronously
- [ ] The check rides the existing 5s Tailscale refresh, not a new timer, and a wedged resolver never stalls a frame or delays the 1s dashboard tick
- [ ] Before the first check completes, and for apps that are not tailnet-exposed, no marker renders
- [ ] The warning wording stays consistent with tickets 09 and 10 rather than being reworded independently
- [ ] Covered by unit tests in the `ui` package via the injectable resolver seam: resolve-ok renders no marker, resolve-fail renders one, unknown renders none
