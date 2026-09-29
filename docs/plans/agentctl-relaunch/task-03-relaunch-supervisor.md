---
id: "03-relaunch-supervisor"
title: "Relaunch agentctl behind a runtime flag"
status: done
wave: 3
depends_on:
  - "01-endpoint-owner"
  - "02-retire-stale-generation"
plan: "plan.md"
requirements:
  - REQ-PLATFORM-AGENT-RUNTIME-AVAILABILITY-001
  - REQ-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002
acceptance_criteria:
  - AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-001.1
  - AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-001.3
  - AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-001.10
  - AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.1
  - AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.2
  - AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.4
  - AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.5
  - AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.9
  - AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.10
system_design:
  - ../../specs/platform/system-design/agent-runtime-availability.md
---

# Task 03: Relaunch agentctl behind a runtime flag

## Summary

Replace the one-way availability latch with the transition table, add the
`agentctlSupervisor` with its backoff and outage window, and gate it behind
`features.agentctlRelaunch`.

## In scope

- `Availability.MarkRecovering`, reversible `MarkAvailable`, and
  `MarkUnavailable(reason, at)` with the `relaunch_exhausted` reason.
- `agentctlSupervisor` in `internal/backendapp` with an injected launcher
  factory, the constants from the system design, generation-tagged exit
  callbacks, and shutdown handling.
- Agent survival: `RecordFreshControlServer` (non-fatal, 2 s timeout) and a
  renewer stopped at loop start and started in step 4, immediately before
  each publish and only when the attempt's `exited` flag is clear. The renewer
  is owned by the supervisor, not an endpoint observer.
- The lock discipline from the system design: nothing waits on a launcher
  (`ForceStop`, `Stop`, exit channel), the renewer drain, or `wg.Wait` while
  holding the supervisor mutex, because `monitorExit` runs the exit closure
  (which takes that mutex) before closing `exited`.
- A 10 s `relaunchAttemptTimeout` context on each `Start`.
- An exported `launcher.Launcher.ForceStop()` that ignores agent survival, used
  for every failed or abandoned attempt.
- The shutdown order and cancellation checkpoints from the system design.
- The runtime flag through the `/runtime-feature-flags` checklist: registry
  entry, `config.Features.AgentctlRelaunch`, `profiles.yaml` (`prod: "false"`,
  `dev: "true"`, `e2e: "true"`), and completeness tests.

## Exclusions

- Browser copy and alert rendering (task 04).
- Adopted control servers.

## Acceptance conditions

1. With the flag on and a fake launcher, one exit publishes `recovering` then
   `available`; five failing attempts publish `unavailable` with
   `relaunch_exhausted`; a fourth outage inside 10 minutes publishes
   `unavailable` without an attempt.
2. With the flag off, one exit publishes `unavailable` with `agentctl_exited`
   and no attempt starts.
3. Cancelling the supervisor during a delay or an attempt force-stops the
   unpublished launcher, publishes nothing further, and leaves no goroutine
   (goleak). Shutdown racing step 3 either force-stops with no transition or
   leaves a published, `available` replacement.
4. An attempt that exceeds the attempt timeout is force-stopped and counts as a
   failed attempt; a failed `RecordFreshControlServer` still publishes.
5. `ForceStop` kills a started process under agent survival without firing
   `OnUnexpectedExit`; it returns at once when no process was started, and it
   sends no signal after `Start`'s own failure cleanup or on a second call.
6. An exit during the step 3 record (the fake launcher calls the exit closure
   before closing its exit channel, as `monitorExit` does) fails the attempt and
   the loop continues, under a test deadline so a deadlock fails the test.
   Shutdown while that closure waits on the mutex completes with no
   transition.

## Verification

```bash
cd apps/backend && go test -race ./internal/agent/runtime/agentctl/... ./internal/backendapp/... ./internal/runtimeflags/... ./internal/profiles/... -count=1
cd apps/backend && golangci-lint run ./... --new-from-rev=main --timeout=5m
cd apps/web && pnpm run typecheck && pnpm exec vitest run lib/state/slices/features
```

## Likely files

- `apps/backend/internal/agent/runtime/agentctl/availability.go` and test
- `apps/backend/internal/backendapp/agentctl.go`
- `apps/backend/internal/backendapp/agentctl_supervisor.go` (new) and test
- `apps/backend/internal/agent/runtime/agentctl/launcher/launcher.go` (`ForceStop`) and test
- `apps/backend/internal/runtimeflags/registry.go`
- `apps/backend/internal/common/config/config.go`
- `apps/backend/internal/profiles/profiles.yaml` (the root `profiles.yaml` is a symlink)
- `apps/web/lib/state/slices/features/types.ts` and any other frontend metadata the `/runtime-feature-flags` skill names

## Risks

- `launcher.Start` blocks up to 30 seconds on health. The attempt timeout and
  shutdown must cancel it through the context, not wait out the deadline.
- `launcher.Launcher` is single-use; every attempt needs a new instance.
- `Launcher.Stop` is a no-op under agent survival. Unpublished attempts use
  `ForceStop` (system design, Ordering and edge rules).
- An attempt's process can exit between a healthy start and publish; test that
  the attempt fails instead of publishing a dead endpoint, and that the exit
  closure and `ForceStop` do not deadlock (system design, Lock discipline).
- The exact frontend runtime-flag test path must come from the
  `/runtime-feature-flags` skill; adjust the vitest filter if it differs.
