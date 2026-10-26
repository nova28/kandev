---
id: "18-process-surface-labels"
title: "Sandbox or label every session process"
status: pending
wave: 11
depends_on:
  - "05-nono-provider-lifecycle"
  - "10-execution-surfaces"
plan: "plan.md"
requirements:
  - REQ-AGENTS-SANDBOX-012
acceptance_criteria:
  - AC-AGENTS-SANDBOX-012.8
system_design:
  - ../../specs/agents/system-design/sandbox-session-conformance.md
---

# Task 18: Sandbox or label every session process

## Summary

Route every process Kandev starts for a session through one function that sandboxes it or records an `unsandboxed` label before it starts.

## Scope

- `ProcessKind` entry point in lifecycle for setup, cleanup, dev processes, language servers and terminals.
- Labels in the launch snapshot and Execution details.
- First delivery: scripts labelled `unsandboxed`; terminals follow the existing transport refusal.

## Acceptance

Each listed criterion in `docs/specs/agents/requirements/sandbox-session-conformance.md` holds as written. A reason code in a refusal is asserted by name in a test.

## Verification

- Unit: each kind is sandboxed or labelled; a kind with unknown state is labelled; label recorded before output.
- Frontend: Execution details shows the label on desktop and phone with seven locales.

## Files likely touched

- `apps/backend/internal/agent/runtime/lifecycle/`
- `apps/backend/internal/scriptengine/providers.go`
- `apps/web/components/task/session-execution-details.tsx`

## Dependencies

Depends on: 05-nono-provider-lifecycle, 10-execution-surfaces. See the dependency table in the system design.

## Risks

Finding every start site is the risk; a lint-style test that fails on a direct process start outside the entry point is part of the work.

## Results

Pending. Design only; no implementation is authorized for this work order yet.
