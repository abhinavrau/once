# Progress rendering stays per-frontend behind DeployProgressCallback

Deploy, update, apply-settings, and Tailscale enable all report progress
through one small interface: `docker.DeployProgressCallback`, receiving
`DeployProgress{Stage, Percentage, Status}`. The depth sits behind that seam -
the pull progress tracker collapses Docker's per-layer JSON stream into a
single monotonic percentage - and each frontend supplies its own adapter: the
CLI renders an inline bar, the TUI install activity a full-screen bar, the
Tailscale form filters waiting statuses into text lines, and the background
runner passes nil because a headless daemon has nothing to render and slog
already reports outcomes. A 2026 architecture review proposed unifying these
adapters into one presentation-neutral progress module both frontends render.
Rejected: `DeployProgress` already is the presentation-neutral vocabulary, so
the proposed module fails the deletion test - remove it and no complexity
reappears, because each frontend still needs its own render loop, and the only
shareable content is stage-to-label strings. Multiple differing adapters at
one seam is evidence the seam is placed right, not a smell; this mirrors the
standing decision that frontend-specific error wording stays per-frontend.
The review's secondary win, "command stops importing ui", is void: command
imports ui inherently to launch the TUI, ui never imports command, and
un-borrowing the one progress widget would cost a new package for nothing.
Do not add a shared progress rendering module, a common callback-to-channel
pump, or a non-nil requirement on the callback; nil is the legitimate headless
adapter and the docker implementation must keep guarding it. What the review
did surface survives as a wiring fix, not architecture: TUI paths that can
pull an image must pass a real callback so the user sees the download
percentage the CLI already shows.
