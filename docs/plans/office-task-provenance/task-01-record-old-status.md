---
id: "01-record-old-status"
title: "Record the prior status on the direct status-update path"
status: pending
wave: 1
depends_on: []
plan: "plan.md"
requirements:
  - REQ-OFFICE-TASK-PROVENANCE-001
  - REQ-OFFICE-TASK-PROVENANCE-003
acceptance_criteria:
  - AC-OFFICE-TASK-PROVENANCE-001.1
  - AC-OFFICE-TASK-PROVENANCE-001.2
  - AC-OFFICE-TASK-PROVENANCE-001.3
  - AC-OFFICE-TASK-PROVENANCE-001.4
  - AC-OFFICE-TASK-PROVENANCE-001.5
  - AC-OFFICE-TASK-PROVENANCE-001.6
  - AC-OFFICE-TASK-PROVENANCE-001.7
  - AC-OFFICE-TASK-PROVENANCE-001.8
  - AC-OFFICE-TASK-PROVENANCE-001.10
  - AC-OFFICE-TASK-PROVENANCE-003.1
  - AC-OFFICE-TASK-PROVENANCE-003.2
  - AC-OFFICE-TASK-PROVENANCE-003.3
  - AC-OFFICE-TASK-PROVENANCE-003.4
  - AC-OFFICE-TASK-PROVENANCE-003.5
system_design:
  - ../../specs/office/system-design/task-provenance-01.md
---

# Task 01: Record the Prior Status on the Direct Status-Update Path

## Summary

Thread the `preStatus` value that `UpdateTaskStatus` already reads into the two
provenance writers, so the activity row's `details` and the
`OfficeTaskStatusChanged` payload both carry `old_status`. No status-change
behavior changes other than what is recorded.

## In scope

- Add the regression tests before the production change.
- Pass `preStatus` to `logTaskStatusChangeActivity` and
  `publishTaskStatusChanged` rather than re-reading it in either.
- Convert the pre-state through `dbStateToOfficeStatus` so both keys share the
  Office canonical alphabet.
- Build the activity `details` JSON with `encoding/json` instead of the current
  `fmt.Sprintf` template, so the key can be omitted when the pre-state is
  unreadable.
- Add `old_status` to the event payload map, empty when unknown.

## Out of scope

- The workflow-move producer in `internal/office/service/event_subscribers.go`,
  which already writes the key and must be left alone (AC-001.6).
- Any change to `deriveTaskTimestamps` or the Completed-timestamp guard.
- The activity feed rendering, which is Task 02.
- Widening the 50-row activity read window.

## Acceptance

- A status change through the direct path writes both `new_status` and
  `old_status` in the canonical alphabet, including when the approval gate
  redirects `done` to `in_review` and when the requested status equals the
  current one.
- An unreadable pre-state omits the `old_status` key entirely and never emits
  `backlog` in its place; the status update still succeeds.
- `timeline[].from` is populated on the task detail response for entries written
  by the direct path, and the existing dashboard suite passes unmodified.
- The newest-first read order, the absence of a tiebreak, the one-row-per-call
  write, and the per-caller pre-status read are all left as they are
  (AC-003.1 through .4). These are negative constraints: the evidence is the
  existing suite passing without edits, not a new assertion.

## Verification

```bash
cd apps/backend
go test ./internal/office/dashboard/ -run 'TestStatusChangeActivity|TestCompletedAtReopen|TestUpdateTaskStatus'
go test ./internal/office/dashboard/... ./internal/office/service/...
make -C . fmt
make -C . test
make -C . lint
```

## Files likely touched

- `apps/backend/internal/office/dashboard/service_tasks.go`
- `apps/backend/internal/office/dashboard/status_change_activity_test.go`

## Dependencies

None.

## Risks

- `dbStateToOfficeStatus("")` returns `"backlog"` (`service_tasks.go:835`).
  Converting the pre-state unconditionally silently fabricates a transition out
  of Backlog. Test the empty case before converting.
- `applyApprovalGate` mutates `req.NewStatus` in place. `preStatus` is read
  before the gate runs; re-reading it inside either writer would capture the
  post-update state instead.

## Parallelism

`parallel-safe` with Task 03: disjoint files, and this work order touches no
schema.

## Inputs

- REQ-OFFICE-TASK-PROVENANCE-001, and AC-003.5 for the best-effort guard.
- System design, "Status transition provenance".
- `service_tasks.go:611-632` (`preStatus`), `:663-681` (the activity writer),
  `:968-985` (the event payload), `:821-840` (the conversion).

## Results

Pending.
