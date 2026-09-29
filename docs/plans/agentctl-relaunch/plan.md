---
created: 2026-09-27
status: in_progress
requirements:
  - REQ-PLATFORM-AGENT-RUNTIME-AVAILABILITY-001
  - REQ-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002
system_design:
  - ../../specs/platform/system-design/agent-runtime-availability.md
legacy_specs: []
---

# Implementation Plan: Local agentctl Relaunch

## Overview

Let the backend restore a crashed standalone `agentctl` by itself, within a
bounded number of attempts, instead of leaving local and worktree executors
dead until a full Kandev restart (issue #3962, option B).

The work first moves every startup copy of the control-server identity behind
one endpoint owner, with no behavior change. It then retires executions bound to
a dead generation, adds the relaunch supervisor and the `recovering` status
behind a runtime flag, and finally shows the new states in the browser with a
real-crash end-to-end test.

Decision: [ADR 2026-09-27 in-process agentctl relaunch](../../decisions/2026-09-27-in-process-agentctl-relaunch.md).

## Scope

### In scope

- The endpoint owner and per-request identity reads for every standalone
  consumer.
- Generation tracking on standalone executions and host-utility instances, with
  stale-entry removal and gateway proxy invalidation.
- The relaunch supervisor, reversible availability, and the
  `features.agentctlRelaunch` runtime flag.
- The `recovering` notice, the `relaunch_exhausted` line, localized copy, and
  desktop and phone coverage.

### Out of scope

The requirement's `## Out of scope` list applies. In particular: adopted control
servers under agent survival (follow-up card 53aedb94), automatic session
resume, macOS orphan cleanup, and configurable limits.

## Dependency order

```text
wave 1: 01-endpoint-owner
wave 2: 02-retire-stale-generation      (depends on 01)
wave 3: 03-relaunch-supervisor          (depends on 01, 02)
wave 4: 04-recovery-ui-and-e2e          (depends on 03)
```

Issue #3961 (port-lease owner safety) is **not** a dependency. Per-instance
listeners live inside the `agentctl` process and its allocator restarts with it,
so no lease crosses a relaunch. See the system design's
[Session behavior](../../specs/platform/system-design/agent-runtime-availability.md#session-behavior).

## ASCII UI preview

The notice uses the existing in-flow slot above route content in
`app-status-surface-provider.tsx`. Structure is required; spacing and icons are
illustrative.

### UI-01: Recovering notice (desktop), any authenticated route, status `recovering`

```text
+--------------------------------------------------------------------------+
| (spinner) Restarting local agent runtime                                 |
|  Agent actions and workspace tools are paused while Kandev restarts the  |
|  local runtime. Your saved data remains safe.                            |
+--------------------------------------------------------------------------+
| route content (last-known data, unchanged)                               |
```

Neutral style, `role="status"`, no action, no dismiss (AC-002.7). It disappears
without a reload on `available` (AC-002.8).

### UI-02: Recovery failed (desktop), status `unavailable`, reason `relaunch_exhausted`

```text
+--------------------------------------------------------------------------+
| (!) Local agent runtime stopped                                          |
|  Agent actions and workspace tools may not update while the local        |
|  runtime is stopped. Your saved data remains safe.                       |
|  Automatic recovery did not succeed.                                     |
|  Restart Kandev fully to recover.                     [ Restart Kandev ] |
+--------------------------------------------------------------------------+
```

Destructive style, `role="alert"`. Identical to today's alert plus the third
line (AC-001.6, AC-001.8). With reason `agentctl_exited` the third line is
absent.

### UI-03: Phone, both states

```text
+------------------------------+
| (spinner) Restarting local   |
| agent runtime                |
| Agent actions and workspace  |
| tools are paused while       |
| Kandev restarts the local    |
| runtime. Your saved data     |
| remains safe.                |
+------------------------------+
| route content (scrolls)      |
|                              |
+------------------------------+
| bottom navigation (fixed)    |
+------------------------------+

+------------------------------+
| (!) Local agent runtime      |
| stopped                      |
| ...body...                   |
| Automatic recovery did not   |
| succeed.                     |
| [      Restart Kandev      ] |  >= 44 px, full width
+------------------------------+
```

Copy and the action stack vertically; no horizontal overflow; no overlap with
bottom navigation or safe areas (AC-001.11).

## Tests

- Go unit tests per work order, run with `-race`. Supervisor timing uses
  `testing/synctest`, never `time.Sleep`.
- Frontend unit tests for the alert states, types, store, and WebSocket
  handler.

## E2E tests

- Client-injected `recovering` and `relaunch_exhausted` states on desktop and
  phone (extends the two existing `agent-runtime-unavailable` specs).
- One real-crash desktop scenario: `SIGKILL` the backend's `agentctl` child,
  observe the notice, observe it clear, then run a mock-agent session to
  completion. Cleanup calls `backend.restart()` so the worker's next test gets a
  fresh backend.

## Work orders

| ID | Title | Wave | Depends on |
| --- | --- | --- | --- |
| [01](task-01-endpoint-owner.md) | Route standalone identity through one endpoint owner | 1 | none |
| [02](task-02-retire-stale-generation.md) | Retire executions bound to a dead agentctl generation | 2 | 01 |
| [03](task-03-relaunch-supervisor.md) | Relaunch agentctl behind a runtime flag | 3 | 01, 02 |
| [04](task-04-recovery-ui-and-e2e.md) | Show recovery states and prove a real crash recovers | 4 | 03 |

## Verification

Each work order lists its exact commands. The package is complete when all four
pass and these also pass:

```bash
make -C apps/backend test lint
cd apps && pnpm --filter @kandev/web lint
cd apps/web && pnpm run typecheck && pnpm run i18n:check
python3 scripts/list-docs.py validate && python3 scripts/lint-spec-files.py --all
```

## Risks

- **Missed consumer.** A consumer that keeps a startup copy would keep failing
  after a relaunch. Work order 01 removes `SetAuthToken` from the executor and
  host-utility manager so a missed caller fails to compile.
- **Resume path reuses a stale execution.** `restoreRecoveredFailedExecution`
  reconnects to a recorded instance port that the replacement can reuse. Work
  order 02 hides stale executions inside `ExecutionStore`, so this path and
  every other lookup see them as absent, and tests a reused port.
- **Real-crash E2E destabilizes the worker.** The worker-scoped backend is
  shared. The spec restarts it in cleanup and is the only test that kills
  `agentctl`.
- **Flag interaction with agent survival.** With both flags on, the relaunch
  must record the new server and swap the renewer. Work order 03 covers it with
  a unit test. Adopted servers stay out of scope.
