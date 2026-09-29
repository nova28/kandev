---
id: "02-retire-stale-generation"
title: "Retire executions bound to a dead agentctl generation"
status: pending
wave: 2
depends_on:
  - "01-endpoint-owner"
plan: "plan.md"
requirements:
  - REQ-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002
acceptance_criteria:
  - AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.6
  - AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.11
system_design:
  - ../../specs/platform/system-design/agent-runtime-availability.md
---

# Task 02: Retire executions bound to a dead agentctl generation

## Summary

Record the endpoint generation on every standalone execution and host-utility
instance. Before an execution or instance is used, drop it when its generation
is older than the current one, so the existing on-demand paths create a fresh
instance on the replacement.

## In scope

- A generation field on `AgentExecution`, set by `StandaloneExecutor` at create
  and recovery.
- The stale check inside `lifecycle.ExecutionStore`: a generation source and a
  retire callback passed to `NewExecutionStore`, every accessor that returns
  executions hiding stale entries, and a re-checked delete under the write lock
  so the retire callback runs exactly once (system design, Stale-generation
  retirement). Per accessor kind: single-entry readers and `WithRLock` upgrade
  to the write lock and re-check; `WithLock` deletes in place, skips `fn`, and
  returns `ErrExecutionNotFound`; `List`/`ListExecutionsForTask` return the live
  set and retire each stale entry. Callbacks always run after every store lock
  is released.
- The Manager's retire callback: `RemoveExecution`'s local cleanup without the
  instance delete call, then the new `SetExecutionRetiredHook`.
- `backendapp` wiring of that hook to `VscodeProxyHandler.InvalidateProxy`,
  `PortProxyHandler.InvalidateSession`, and `TunnelManager.InvalidateSession`.
- A generation field on host-utility cached instances, dropped through
  `dropStaleInstance`.

## Exclusions

- Automatic resume of any session.
- Non-standalone executors, which have no endpoint generation.

## Acceptance conditions

1. After a publish, prompting a session whose execution has an older generation
   creates a new instance through the current endpoint and never calls the old
   client.
2. A stale execution whose recorded instance port equals a live instance port
   on the new generation is removed, not reconnected.
3. Retiring a stale execution invalidates its cached gateway proxies and
   tunnels through the retirement hook.
4. Concurrent lookups of one stale execution run the retire callback exactly
   once under `-race`, and a generation-0 execution is never retired.
5. `WithLock` and `WithRLock` on a stale execution return `ErrExecutionNotFound`
   without calling `fn`; a list call over two stale entries returns only live
   entries and runs two callbacks after unlocking, and a callback that reads
   the store does not deadlock.

## Verification

```bash
cd apps/backend && go test -race ./internal/agent/runtime/lifecycle/... ./internal/agent/hostutility/... ./internal/gateway/websocket/... -count=1
cd apps/backend && golangci-lint run ./... --new-from-rev=main --timeout=5m
```

## Likely files

- `apps/backend/internal/agent/runtime/lifecycle/types.go`
- `apps/backend/internal/agent/runtime/lifecycle/executor_standalone.go`
- `apps/backend/internal/agent/runtime/lifecycle/manager_interaction.go`
- `apps/backend/internal/agent/runtime/lifecycle/manager_execution.go`
- `apps/backend/internal/agent/runtime/lifecycle/execution_store.go` and test
- `apps/backend/internal/backendapp/main.go` (hook wiring)
- `apps/backend/internal/agent/hostutility/manager.go`
- `apps/backend/internal/gateway/websocket/vscode_proxy.go`, `port_proxy.go`, `port_tunnel.go`

## Risks

- Removing an execution must follow the lifecycle callback identity rule in
  `apps/backend/AGENTS.md`: a delayed callback from the removed execution must
  not mutate its successor.
- The stale check must run before any I/O on the execution's client.
