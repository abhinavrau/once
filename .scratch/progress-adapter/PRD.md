# PRD - Progress adapter (candidate 07, shrunk)

## Outcome of the grilling

Candidate 07 proposed one presentation-neutral progress module that both
frontends render, unifying the four adapters over
`docker.DeployProgressCallback` and closing the two nil paths. The grilling
**rejected the module** and shrank the candidate to a single TUI wiring fix.
Rationale (full version in `docs/adr/0012-progress-rendering-per-frontend.md`):

- `docker.DeployProgress{Stage, Percentage, Status}` already is the
  presentation-neutral interface. The depth sits behind it: the pull progress
  tracker collapses Docker's per-layer JSON stream into one monotonic
  percentage. Small interface, deep implementation - the seam is healthy.
- The "four incompatible adapters" are four frontends rendering one interface.
  Multiple adapters at one seam is what a real seam looks like, not a smell.
  A shared rendering module fails the deletion test: delete it and no
  complexity reappears, because each frontend still needs its own render loop.
- The claimed win "command stops importing ui" is void: `command` imports `ui`
  inherently to launch the TUI (`ui.Run`), and `ui` never imports `command`,
  so there is no cycle. Un-borrowing the progress widget would need a new
  package for one widget - churn.
- The callback-to-channel pumps in the CLI progress, install activity, and
  Tailscale form differ in buffer size, select shape, and payload. Extracting
  a shared pump would be a shallow pass-through helper. Not worth it.
- The nil path in the background runner is correct: nil is the legitimate
  adapter for a frontend with no display; slog reports outcomes.

## What survives: the TUI loses pull percentage where the CLI keeps it

Two TUI paths pass nil where an image pull can happen, so the user watches an
indeterminate bar for what can be a multi-gigabyte, minutes-long download:

1. Settings apply: the Application section has an editable image field, and
   the apply lifecycle deliberately rolls through Deploy because the new
   Configuration may carry a new image ref that must be pulled.
2. Check for updates: the update action always pulls to detect a change.

The CLI shows a live percentage for both operations, and the TUI itself
already renders pull percentage on install. The fix is wiring, local to the
TUI settings model, on top of the seam that already exists.

## Decided constraints (do not re-argue)

- No unified progress module; progress rendering stays per-frontend, parallel
  to the decided "frontend-specific error wording stays per-frontend" rule.
- `command`'s import of `ui` stays.
- The background runner keeps passing nil.
- The action signature carried by the settings action message changes to
  accept a `docker.DeployProgressCallback` - the same task shape the CLI's
  `runWithProgress` already uses; actions that report no progress ignore it.

## Cross-feature dependencies

None. Candidate 01's apply-settings spine has landed on main (the TUI already
calls `Namespace.ApplySettings`), so this wiring sits on top of it.

## Tickets

- 01 - TUI shows pull percentage on settings apply and update check
