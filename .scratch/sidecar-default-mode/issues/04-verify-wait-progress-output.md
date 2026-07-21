# 04 - Progress output during the post-enable verify wait

**What to build:** After booting the topology, enable verifies every expected
service is advertised by the local daemon and approved in the tailnet, polling
for up to 90 seconds per phase with no output. A user watching it cannot tell
a healthy slow tailnet from a wedged enable - in the motivating incident the
user Ctrl-C'd a run that would have surfaced its error at ~94s. Print what the
verify is waiting for (and the bound) when each wait phase begins, so enable
never looks hung. Outside ADR-0007's scope; independent of the mode work.

**Blocked by:** None - can start immediately.

**Status:** in-review

- [x] When the advertise wait begins, enable prints a line naming the services being waited on and the time bound
- [x] When the approval wait begins, enable prints a corresponding line
- [x] Waits that resolve immediately do not spam extra output beyond the single line per phase
- [x] Success and failure messages are unchanged; timeout errors still name the still-missing services and point at the docktail container logs
