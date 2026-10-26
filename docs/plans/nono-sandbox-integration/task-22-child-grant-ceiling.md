---
id: "22-child-grant-ceiling"
title: "Refuse a child task wider than its parent's guard"
status: pending
wave: 11
depends_on:
  - "04-policy-authority"
  - "11-guarded-session-surface"
plan: "plan.md"
requirements:
  - REQ-AGENTS-SANDBOX-012
acceptance_criteria:
  - AC-AGENTS-SANDBOX-012.15
system_design:
  - ../../specs/agents/system-design/sandbox-session-conformance.md
---

# Task 22: Refuse a child task wider than its parent's guard

## Summary

Check requested child grants against the parent guard at creation, in the child-insert transaction, and refuse with `sandbox_outside_ceiling`.

## Scope

- Optional `sandbox_grants` object on task creation; the pre-insert `InheritTaskGuard` and `AdmitTask` hook in `sandbox_creation.go` moves inside the insert transaction; typed error `sandbox_outside_ceiling`; role-and-access containment; inherit-exactly for a child requesting nothing; no ceiling for an unguarded parent.
- Parent guard read in the insert transaction; launch-time `checkParentGuard` stays.
- `sandbox_grants` request shape, `narrowing` stored in `sandbox_task_guards`, effective policy compiled at launch, `sandbox_grants_invalid` and `sandbox_grants_without_guard`, caller-task source kept, REST and MCP create-task surfaces, lock order workspace, step, parent row, new row.

## Acceptance

Each listed criterion in `docs/specs/agents/requirements/sandbox-session-conformance.md` holds as written. A reason code in a refusal is asserted by name in a test.

## Verification

- Unit: wider grant refused with no child row; equal and narrower accepted; empty request inherits; unguarded parent; archived parent keeps its existing not-found error; two concurrent creations each evaluated at their own commit.

## Files likely touched

- `apps/backend/internal/agent/sandbox/task_admission.go`
- `apps/backend/internal/task/service/`

## Dependencies

Depends on: 04-policy-authority, 11-guarded-session-surface. See the dependency table in the plan.

## Risks

Task creation lives outside the sandbox package; the transaction boundary must be shared.

## Results

Pending. Design only; no implementation is authorized for this work order yet.
