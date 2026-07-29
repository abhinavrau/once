# 05 - Enable Funnel spine on Namespace

**What to build:** Opening a Funnel is the only workflow with no shared spine at
all: the CLI and the TUI each run the same six steps, written twice, with
different wording for the same failure and a helper that is duplicated
byte-for-byte across the two packages along with the constant it depends on.

Give Namespace an Enable Funnel entry point owning: validate the requested
duration, confirm Tailscale is enabled, confirm the background daemon is running,
enable the Funnel on the Application with its expiry, and wait for the local
tailscaled to confirm the Funnel is actually serving before reporting success. It
returns the public URL and the expiry time.

Two behaviour fixes fall out of the move:

- The TUI currently decides whether Tailscale is enabled when the Funnel form is
  built and carries that answer to submit time, so the check can be stale. The
  spine checks live. The form keeps its own enabled flag for the status line it
  renders, which is a display concern evaluated at build time on purpose.
- The two frontends report the same not-enabled condition with different
  sentences. The spine returns a sentinel and each frontend supplies its own
  wording, because the right affordance genuinely differs: a TUI cannot tell the
  user to run a CLI command, and a CLI cannot tell them to press a key on the
  dashboard.

The background-daemon precondition moves into the engine as part of this ticket,
deleting both copies and the duplicated service-name suffix. This is the one
change that adds a package edge from the engine to the service-management
package. The edge is acyclic - that package imports nothing internal - and the
engine already owns a daemon precondition of its own for Tailscale enable. The
precondition must keep asking the service manager whether the daemon is running,
not probe the admin socket: the socket server restarts with backoff independently
of the loop that tears Funnels down at expiry, so a socket probe would refuse
legitimate Funnels.

Funnel disable stays as it is. It is a guard and a call, its messages are
frontend-specific, and a spine for it would be a pass-through.

**Blocked by:** 01 - TUI surfaces settings-deploy failures honestly.

**Status:** ready-for-agent

- [ ] Namespace exposes one Enable Funnel entry point returning the public URL, the expiry, and an error
- [ ] The CLI funnel enable command and the TUI Funnel form both call it and contain no ordering of their own
- [ ] Enabling a Funnel with Tailscale disabled fails on both paths, each with wording appropriate to its interface
- [ ] The Tailscale-enabled check reflects the state at submit time, not at form-build time
- [ ] Enabling a Funnel without the background daemon running fails on both paths, naming the install command
- [ ] The daemon check consults the service manager and is not satisfied merely by the admin socket
- [ ] The background-daemon helper and the service-name suffix exist in exactly one place
- [ ] A Funnel that never activates reports the activation failure rather than reporting success
- [ ] Funnel disable behaviour is unchanged on all three of its callers, including expiry teardown
- [ ] Precondition ordering is unit-tested without a Docker daemon: an invalid duration is rejected before the daemon and Tailscale checks
- [ ] Integration tests pass
