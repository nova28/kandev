---
id: "04-recovery-ui-and-e2e"
title: "Show recovery states and prove a real crash recovers"
status: done
wave: 4
depends_on:
  - "03-relaunch-supervisor"
plan: "plan.md"
requirements:
  - REQ-PLATFORM-AGENT-RUNTIME-AVAILABILITY-001
  - REQ-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002
acceptance_criteria:
  - AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-001.6
  - AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-001.9
  - AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-001.11
  - AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.2
  - AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.3
  - AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.7
  - AC-PLATFORM-AGENT-RUNTIME-AVAILABILITY-002.8
system_design:
  - ../../specs/platform/system-design/agent-runtime-availability.md
---

# Task 04: Show recovery states and prove a real crash recovers

## Summary

Render the `recovering` notice and the `relaunch_exhausted` line, localize the
copy, and add end-to-end coverage, including one scenario that kills the real
`agentctl` and runs a session afterwards.

## In scope

- `lib/types/agent-runtime.ts`: the `recovering` variant and the
  `relaunch_exhausted` reason. The store and WebSocket handler ignore an
  unknown status.
- `AgentRuntimeStatusAlert` (renamed from `AgentRuntimeUnavailableAlert`) with
  `data-status`, per the plan's UI-01 to UI-03.
- Keys `agentRuntimeRecoveringTitle`, `agentRuntimeRecoveringBody`, and
  `agentRuntimeRelaunchExhausted` in en, ja, pt-pt, zh-cn, zh-hk, zh-tw, and
  pseudo. Use `pnpm run i18n:zh-hant` for the Traditional Chinese pair. No em
  dash.
- Playwright: extend `agent-runtime-unavailable.spec.ts` and
  `mobile-agent-runtime-unavailable.spec.ts` with injected `recovering` and
  `relaunch_exhausted` states; add `agent-runtime-relaunch.spec.ts` for the real
  crash.

## Exclusions

- Backend behavior (task 03).
- A recovered toast.

## ASCII UI preview

See [plan UI-01 to UI-03](plan.md#ascii-ui-preview). UI-01 is the recovering
notice (neutral, `role="status"`, no action). UI-02 adds the
"Automatic recovery did not succeed." line to the existing alert. UI-03 stacks
both on phone with a full-width 44 px action.

## Acceptance conditions

1. Unit tests: `recovering` renders the notice with no button; `unavailable`
   with `relaunch_exhausted` renders the extra line and the restart action;
   `available`, `null`, and an unknown status render nothing.
2. Injected-state Playwright on desktop and phone: the notice shows over
   last-known route data, is not dismissible, clears on `available` without a
   reload, and meets the phone geometry rules.
3. Real-crash Playwright: find the `agentctl` child with `pgrep -P <backend
   pid>`, send `SIGKILL`, wait for `data-status="recovering"`, wait for the
   alert to detach, then launch a mock-agent session on the local executor and
   wait for its completion. Cleanup calls `backend.restart()`. No sleeps; use
   Playwright waits.

## Verification

```bash
cd apps/web && pnpm exec vitest run components/app-status-bar lib/ws/handlers/system-events.test.ts lib/state/store.test.ts
cd apps/web && pnpm run typecheck && pnpm run i18n:check
cd apps && pnpm --filter @kandev/web lint
cd apps/web && pnpm e2e:run tests/layout/agent-runtime-unavailable.spec.ts tests/layout/mobile-agent-runtime-unavailable.spec.ts tests/layout/agent-runtime-relaunch.spec.ts
```

## Likely files

- `apps/web/lib/types/agent-runtime.ts`
- `apps/web/components/app-status-bar/agent-runtime-unavailable-alert.tsx` (rename) and test
- `apps/web/components/app-status-bar/app-status-surface-provider.tsx`
- `apps/web/lib/ws/handlers/system-events.ts`
- `apps/web/src/locales/*/system.json`
- `apps/web/e2e/helpers/agent-runtime-availability.ts`
- `apps/web/e2e/tests/layout/*agent-runtime*.spec.ts`

## Risks

- `pgrep -P` depends on the launcher keeping `agentctl` as a direct child of
  the backend. If the backend fixture wraps the process, resolve the child from
  the backend log line that records the `agentctl` PID instead.
- The real-crash spec relies on `e2e: "true"` for the flag in `profiles.yaml`.
