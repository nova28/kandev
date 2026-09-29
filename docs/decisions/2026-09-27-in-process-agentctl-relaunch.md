# ADR-2026-09-27-in-process-agentctl-relaunch: Relaunch a crashed standalone agentctl through one endpoint owner

**Status:** proposed
**Date:** 2026-09-27
**Area:** backend, frontend, protocol, operations

## Context

[ADR-2026-08-08-agentctl-crash-containment](2026-08-08-agentctl-crash-containment.md)
made local agent-runtime availability monotonic within one backend boot. After
an unexpected standalone `agentctl` exit, every local and worktree session stays
unusable until the operator restarts all of Kandev. That ADR deferred an
in-process restart: the per-launch auth token, port, and PID are copied by value
into several independent owners at startup, so replacing the process without
rebinding all of them together would leave a runtime that looks healthy while
every call fails authentication.

Issue [kdlbs/kandev#3962](https://github.com/kdlbs/kandev/issues/3962) reports
the consequence in practice: the operator did not find the restart path and
rebooted the machine. Local and worktree executors are the default, so one
child crash stops all default agent work.

Prior art is consistent on one point. Rivet agentOS does not restart a crashed
adapter or replay an uncertain prompt; a later explicit prompt restores the
durable session. Multica fails the tasks that were running on a lost daemon and
lets the restarted daemon reclaim them. Neither silently resumes interrupted
agent work.

## Decision

1. **One endpoint owner.** A single backend component owns the standalone
   control-server identity (host, port, auth token, PID, and a monotonically
   increasing generation). Every consumer that today captures those values at
   startup reads them from this owner for each request instead. Publishing a new
   generation replaces all four values in one step; no reader can observe a new
   token with an old port or PID.
2. **Bounded relaunch.** When the launcher reports an unexpected exit, the owner
   starts a replacement `agentctl` with a fixed backoff schedule. A replacement
   counts only after the existing health check and bootstrap-nonce handshake
   succeed. The owner publishes the new generation, restores dependent loops
   (ownership renewal and the agent-survival adoption record), and only then
   reports the runtime available.
3. **Crash-loop limit.** A fixed per-outage attempt limit and a fixed window on
   repeated outages end relaunching. The runtime then enters the same terminal
   unavailable state as before, now with a distinct reason, and the existing
   **Restart Kandev** recovery applies.
4. **Reversible availability with a recovering state.** The install-wide
   availability snapshot gains a `recovering` status. Transitions are
   `available → recovering → available` on success and
   `recovering → unavailable` on exhaustion. `unavailable` remains terminal
   within one backend boot.
5. **No silent session restart.** Relaunch restores the runtime, not the work.
   Sessions that were running on the lost process follow the existing
   interruption, resume, and terminalization paths. A later explicit user
   action resumes them.
6. **Staged rollout.** Relaunch sits behind a restart-required runtime feature
   flag that is off in the production profile. With the flag off, behavior is
   exactly the 2026-08-08 contract.

This supersedes the "availability is monotonic within one backend boot" part of
the 2026-08-08 ADR, and its deferral of in-process restart. The immutable ACP
event snapshot decision and the shell-level alert decision still stand.

The observable behavior is specified in
[`docs/specs/platform/requirements/agent-runtime-availability.md`](../specs/platform/requirements/agent-runtime-availability.md)
(`REQ-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002`). The mechanism is in
[`docs/specs/platform/system-design/agent-runtime-availability.md`](../specs/platform/system-design/agent-runtime-availability.md).

## Consequences

- A single agentctl crash no longer stops all default agent work until an
  operator restarts Kandev. Recovery normally completes within seconds.
- Consumers pay one synchronized read of the endpoint per request, in exchange
  for removing every startup-time copy of the credential.
- A request built against an old generation can fail once during the swap. It
  surfaces its error and is not retried by the endpoint owner.
- On macOS and BSD, agent processes orphaned by the crashed `agentctl` keep
  running after the relaunch. This ADR does not add orphan cleanup.
- An adopted control server (agent survival) has no exit monitor today, so it
  is not relaunched. Detecting its loss is separate work.

## Alternatives Considered

### Keep monotonic availability and improve the restart guidance

Rejected. The full restart already exists and is offered in the alert, yet the
reported outage still ended in a machine reboot. It also discards every healthy
backend subsystem to recover one child process.

### Restart inside `monitorExit` and update `cfg.Agent` fields

Rejected. Consumers copied the old values at startup and never read the
configuration again, so the process would be alive while every call returned
401 and availability reported it down: worse than today.

### Call per-owner setters in sequence after a relaunch

Rejected. Each setter would publish at a different moment, leaving windows where
the executor, host-utility manager, and renewal loop disagree about the token.
A single generation read by all consumers removes the ordering problem.

### Unlimited automatic retries

Rejected. A deterministic crash, such as a broken binary or a port the host
cannot bind, would loop forever and hide the failure behind a permanent
"recovering" state.

### Automatically resume sessions that were running

Rejected. The interrupted turn's outcome is uncertain. Replaying a prompt can
duplicate side effects, which matches the prior-art consensus above.
