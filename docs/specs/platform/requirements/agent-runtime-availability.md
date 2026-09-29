---
status: draft
system: platform
created: 2026-08-08
updated: 2026-09-27
owners:
  - kandev
---
# Agent Runtime Availability Requirements

## Overview

Kandev's standalone `agentctl` process is a core runtime dependency for local
and worktree executors. The application backend can remain reachable after that
child exits. In that state the browser WebSocket still looks connected while
agent actions, task updates, and workspace tools stop progressing.

Users need an authoritative explanation, last-known data that stays visible,
and a recovery path. REQ-001 defines the availability snapshot and the terminal
outage alert. REQ-002 lets Kandev restore the runtime by itself after an
unexpected exit, within a bounded number of attempts, when the relaunch
capability is enabled.

## Terms

- **Local agent runtime:** the standalone `agentctl` process that the backend
  starts for local and worktree executors.
- **Outage:** the interval that starts with an unexpected exit of the local
  agent runtime and ends when a replacement is available or when Kandev gives
  up.
- **Relaunch capability:** the restart-required runtime feature toggle
  `features.agentctlRelaunch` (`KANDEV_FEATURES_AGENTCTL_RELAUNCH`).
- **Terminal outage:** an outage that only a full Kandev restart ends.

## Requirements

### REQ-PLATFORM-AGENT-RUNTIME-AVAILABILITY-001: Agent Runtime Availability

**Intent:** Every browser shows an accurate, persistent explanation when the
local agent runtime has stopped, keeps last-known data visible, and offers the
supported full-restart path for a terminal outage.

#### Acceptance criteria

- **AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-001.1:** Kandev exposes one
  install-wide local agent-runtime availability snapshot whose status is
  `available`, `recovering`, or `unavailable`. The status `recovering` occurs
  only under REQ-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.
- **AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-001.2:** The runtime becomes
  `available` only after a standalone `agentctl` process passes its health check
  and authentication handshake.
- **AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-001.3:** When the relaunch capability
  is disabled and `agentctl` exits unexpectedly, the system shall atomically
  change the snapshot to `unavailable`, record the stable reason
  `agentctl_exited` and the occurrence time, and publish that snapshot to
  connected browsers.
- **AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-001.4:** Intentional shutdown does
  not publish an outage.
- **AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-001.5:** A newly loaded or
  reconnected browser receives the latest snapshot even when it missed the
  original transition event.
- **AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-001.6:** While the status is
  `unavailable`, every authenticated application route shows one persistent,
  non-dismissible alert. It explains that the local agent runtime stopped, agent
  actions and workspace tools may not update, saved data remains intact, and
  Kandev must restart to recover. When the reason is `relaunch_exhausted`, the
  alert also states that automatic recovery did not succeed.
- **AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-001.7:** The alert is independent of
  the **Show status bar** Appearance preference and remains visible when route
  data is empty or stale. It is not a transient toast.
- **AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-001.8:** When the local restart
  capability is supported, the `unavailable` alert offers **Restart Kandev** and
  reuses the existing supervised restart progress flow. Otherwise it gives
  localized terminal or service-manager guidance. A failed restart request
  leaves the alert visible and exposes the failure.
- **AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-001.9:** When the availability
  snapshot changes to any status, the browser shall keep its last-known task,
  session, workspace, and message data. An availability event never clears
  those stores.
- **AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-001.10:** When the status is
  `unavailable`, the system shall keep it `unavailable` for the rest of that
  backend boot. Browser WebSocket reconnection alone does not clear the alert; a
  fresh backend boot with a healthy authenticated `agentctl` does.
- **AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-001.11:** On a phone viewport the
  alert renders in flow above route content, not as a toast, drawer, or second
  bottom bar. Its copy and actions stack vertically, any action is at least
  44 px tall, and it causes no horizontal document overflow and no overlap with
  bottom navigation or safe-area content.
- **AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-001.12:** The snapshot exposes only
  the stable status, the stable reason, and the occurrence time. Raw process
  errors, exit codes, PIDs, ports, and credentials never appear in a browser
  payload.

### REQ-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002: Automatic Local Runtime Recovery

**Intent:** As a Kandev operator, I want a crashed local agent runtime to come
back by itself, so that one child-process crash does not stop all local agent
work until I find and use a full restart.

#### Acceptance criteria

- **AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.1:** When the relaunch
  capability is enabled and an available `agentctl` exits unexpectedly, the
  system shall publish the status `recovering` with the reason `agentctl_exited`
  and the exit time, and start a replacement `agentctl` without an operator
  action.
- **AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.2:** When a replacement passes
  its health check and authentication handshake, the system shall publish
  `available`. The first replacement attempt starts no later than 2 seconds
  after the exit. When that attempt succeeds, the system publishes `available`
  within 15 seconds of the exit.
- **AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.3:** When the runtime is
  `available` again after a recovery, the system shall accept a new local or
  worktree session launch and run it to completion, and every other local
  runtime operation (workspace files, shell, Git, ports, and host utilities)
  shall work without a Kandev restart.
- **AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.4:** When a replacement attempt
  fails, the system shall retry after a backoff of 1, 2, 4, and 8 seconds, for
  at most 5 attempts per outage. When the fifth attempt fails, the system shall
  publish `unavailable` with the reason `relaunch_exhausted`.
- **AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.5:** When an unexpected exit
  would start the fourth outage within 10 minutes of the first of those
  outages, the system shall not attempt a replacement and shall publish
  `unavailable` with the reason `relaunch_exhausted`.
- **AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.6:** When the runtime recovers,
  the system shall not restart, resume, or replay any session that was running
  on the exited process. Those sessions show their existing interruption error.
  When the user later resumes or prompts one of them, it runs on the
  replacement runtime.
- **AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.7:** While the status is
  `recovering`, every authenticated application route shows one persistent,
  non-dismissible notice. It states that Kandev is restarting the local agent
  runtime, that agent actions and workspace tools are paused, and that saved
  data remains intact. It offers no restart action. It follows AC-001.7 and
  AC-001.11 for placement.
- **AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.8:** When the status changes from
  `recovering` to `available`, the notice disappears from every connected
  browser without a page reload.
- **AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.9:** When Kandev shuts down
  intentionally during `recovering`, the system shall stop relaunching, stop any
  replacement it started but has not yet made current, and publish no further
  availability transition. A replacement that became current before shutdown
  began is treated like any current runtime: it keeps running when agent
  survival is enabled and is stopped otherwise.
- **AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.10:** When the relaunch
  capability is disabled, availability behavior is exactly REQ-001: an
  unexpected exit publishes `unavailable` with the reason `agentctl_exited`, and
  no replacement is started.
- **AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.11:** When a request to the local
  runtime was sent to the exited process, it fails with its existing error
  surface and is not retried automatically. A new local or worktree session
  launch, or a workspace file, shell, Git, port, or host-utility operation,
  that is sent before the replacement becomes current fails with its existing
  error and is not queued. One sent after the replacement becomes current
  reaches the replacement, even if the `recovering` notice has not cleared yet.

## Out of scope

- Detecting or relaunching an adopted control server under agent survival
  (follow-up card 53aedb94). This requirement covers the process that the
  current backend boot started.
- Restarting, resuming, or replaying interrupted agent work automatically.
- Cleaning up agent processes that the exited `agentctl` orphaned on macOS and
  BSD.
- Operator-configurable attempt limits, backoff, or window. These are fixed.
- An attempt counter, countdown, or raw diagnostics in the UI.
- A one-time "runtime recovered" toast; interrupted sessions already show their
  own error.
- Changing `/health` readiness semantics, container or service restart
  policies, or browser WebSocket retry behavior.
- Persisted crash history, crash counters, or a new operational dashboard.
- Declaring remote executors unavailable or disabling every application
  action. The notice and alert describe the local standalone runtime only.

## Decision record

- [Publish immutable agent events and explicit runtime failure state](../../../decisions/2026-08-08-agentctl-crash-containment.md)
- [Relaunch a crashed standalone agentctl through one endpoint owner](../../../decisions/2026-09-27-in-process-agentctl-relaunch.md)

## System design

[Agent Runtime Availability System Design](../system-design/agent-runtime-availability.md)

## Implementation plans

- [Backend failure containment](../../../plans/backend-failure-containment/plan.md)
  (REQ-001, completed)
- [Local agentctl relaunch](../../../plans/agentctl-relaunch/plan.md) (REQ-002)
