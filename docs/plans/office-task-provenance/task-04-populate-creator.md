---
id: "04-populate-creator"
title: "Populate the creator pair at every task-creation entry point"
status: pending
wave: 2
depends_on: ["03-creator-columns"]
plan: "plan.md"
requirements:
  - REQ-OFFICE-TASK-PROVENANCE-002
acceptance_criteria:
  - AC-OFFICE-TASK-PROVENANCE-002.4
  - AC-OFFICE-TASK-PROVENANCE-002.5
  - AC-OFFICE-TASK-PROVENANCE-002.7
  - AC-OFFICE-TASK-PROVENANCE-002.8
  - AC-OFFICE-TASK-PROVENANCE-002.9
  - AC-OFFICE-TASK-PROVENANCE-002.10
  - AC-OFFICE-TASK-PROVENANCE-002.11
system_design:
  - ../../specs/office/system-design/task-provenance-01.md
---

# Task 04: Populate the Creator Pair at Every Task-Creation Entry Point

## Summary

Add the creator pair to `taskservice.CreateTaskRequest` and supply it from each
production creation entry point per the system design's mapping table. The
service persists what it is given and never infers a creator from request
context.

## In scope

- Add the mapping tests before the production change.
- Add `CreatedByType` / `CreatedByID` to `taskservice.CreateTaskRequest` and
  persist them on the creating INSERT only.
- Populate them at each entry point in the system design's table: the Office
  agent action, agent child-task delegation, both `create_task_kandev` stream
  kinds, web UI creation, routine, automation, plugin host write, and
  onboarding.
- Collapse a half-identity to `("", "")` before persisting.

## Out of scope

- Deriving the creator inside the task service from `authn.Identity`, which is
  explicitly forbidden by AC-002.7.
- Any read path, DTO field, or UI, which is Task 05.
- Changing `tasks.origin` or reconciling its vocabulary with
  `created_by_type`.

## Acceptance

- Each entry point in the mapping table stores the pair named for it, with the
  agent rows using the `agent_profiles` id space and no second id space
  introduced.
- A type with a missing id, or an id with no type, is stored as `("", "")`; a
  `system` creator with an empty id is stored as-is.
- An `external_id` collision returns the pre-existing task with its original
  creator intact, and no update path modifies either column.

## Verification

```bash
cd apps/backend
go test ./internal/task/service/... ./internal/backendapp/... ./internal/office/runtime/...
go test ./internal/plugins/... ./internal/task/handlers/...
make -C . fmt
make -C . test
make -C . lint
```

## Files likely touched

- `apps/backend/internal/task/service/service_requests.go`
- `apps/backend/internal/task/service/service_tasks.go`
- `apps/backend/internal/task/service/service_child_task.go`
- `apps/backend/internal/backendapp/{adapters_office.go,orchestrator.go,canvas_routes.go,services.go}`
- `apps/backend/internal/task/handlers/{task_http_handlers.go,task_ws_handlers.go}`
- `apps/backend/internal/office/runtime/actions.go`
- `apps/backend/internal/plugins/host_write.go`

## Dependencies

Task 03 — the columns must survive the rebuild before anything writes to them.

## Risks

- **A context-derived creator is confidently wrong.** `internal/mcp/scope/scope.go`
  mints the *workspace owner's* identity for an in-session agent stream, so a
  single service-level default reading `authn.Identity` would attribute every
  agent-created task to a human and would pass casual review. Check each call
  site against the mapping table.
- `CreateChildTask` has only `parent *models.Task` and a `ChildTaskSpec` — no
  session or run context. Use `parent.AssigneeAgentProfileID`, and store
  `("", "")` when the parent has no runner. Never substitute the *new* task's
  assignee, which is the agent that will do the work, not the one that created
  it.
- Roughly ten production call sites exist. One missed site is not a build
  failure; it silently stores an empty pair, which is why the mapping tests
  matter more than compilation here.

## Parallelism

`sequential`

## Inputs

- AC-OFFICE-TASK-PROVENANCE-002.4, .5, .7 through .11.
- System design, "Entry-point mapping" and "Why the creator is passed in".
- `service_requests.go:72`, and the call sites listed in the plan.

## Results

Pending.
