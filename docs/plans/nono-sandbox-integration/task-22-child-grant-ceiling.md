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

Check the child's resolved policy against the narrower of the parent and calling-task guards at creation, in the child-insert transaction, and refuse with `sandbox_outside_ceiling`. Requesting narrower grants is Out of scope.

## Scope

- The pre-insert `InheritTaskGuard` and `AdmitTask` hook in `sandbox_creation.go` moves inside the insert transaction; typed error `sandbox_outside_ceiling`; containment over every guard dimension via `noBroader` and `WithinCeiling`; no ceiling for an unguarded source.
- Parent guard read in the insert transaction; launch-time `checkParentGuard` stays.
- Source task rows (parent, and caller for an MCP-created task) locked in ascending id before reading guards; the guard update takes the same lock and bumps the guard version; lock order workspace, step, source rows, new row.

## Acceptance

Each listed criterion in `docs/specs/agents/requirements/sandbox-session-conformance.md` holds as written. A reason code in a refusal is asserted by name in a test.

## Verification

- Unit: wider resolved policy refused with no child row; equal and narrower accepted; unguarded parent; parent and caller guards both applied; archived parent keeps its existing not-found error; two concurrent creations each evaluated at their own commit.

## Files likely touched

- `apps/backend/internal/agent/sandbox/task_admission.go`
- `apps/backend/internal/task/service/`

## Dependencies

Depends on: 04-policy-authority, 11-guarded-session-surface. See the dependency table in the plan.

## Risks

Task creation lives outside the sandbox package; the transaction boundary must be shared.

## Results

Pending. Design only; no implementation is authorized for this work order yet.
