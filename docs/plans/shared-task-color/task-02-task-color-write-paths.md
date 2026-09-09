---
id: "02-task-color-write-paths"
title: "Accept colour on the task create and update paths"
status: pending
wave: 2
depends_on: ["01-persist-task-color"]
plan: "plan.md"
spec: "../../specs/shared-task-color/spec.md"
acceptance_criteria:
  - AC-3
  - AC-4
  - AC-5
  - AC-6
  - AC-7
  - AC-8
  - AC-17
  - AC-19
---

# Task 02: Accept colour on the task create and update paths

## Summary

Add `color` to the task create request, the HTTP `PATCH`, the WebSocket update
action, and the service request they share, validated against the single
existing palette definition before any mutation. This is the work order that
makes a colour settable with no browser involved.

## In scope

- `Color *string` on `service.UpdateTaskRequest` and `Color string` on
  `service.CreateTaskRequest`.
- `Color` on `httpUpdateTaskRequest`, `httpCreateTaskRequest`, and
  `wsUpdateTaskRequest`, with pass-through to the service.
- A `ValidateTaskColor` wrapper delegating to
  `usermodels.IsValidSidebarTaskColor`, called beside `ValidateTaskPriority` in
  both the update and create paths, before any field is applied.

## Out of scope

- Office (Task 03) and every frontend surface (Tasks 04, 05).
- The dead `pkg/api/v1.CreateTaskRequest` / `UpdateTaskRequest` DTOs, which are
  not on any live path.
- Any revision token or CAS on the task update path.

## Acceptance

- Omitting `color` leaves the stored value unchanged; `""` clears it; a valid
  token sets it.
- An invalid or mis-cased token returns `400` and applies no other field from
  the same request.
- The HTTP and WebSocket paths behave identically, and each accepted write
  advances `updated_at` and publishes `task.updated` even when the value is
  unchanged.

## Verification

```bash
(cd apps/backend && go test ./internal/task/...)
make -C apps/backend lint
```

## Files likely touched

- `apps/backend/internal/task/service/service_requests.go`
- `apps/backend/internal/task/service/service_tasks.go`
- `apps/backend/internal/task/handlers/task_http_handlers.go`
- `apps/backend/internal/task/handlers/task_ws_handlers.go`
- `apps/backend/internal/task/service/service_task_color_test.go` (new)

## Dependencies

Task 01 — the column and model field must exist before anything can write them.

## Risks

- Defining a second seven-token list in the task package passes every test and
  violates AC-17; delegate to `usermodels.IsValidSidebarTaskColor`.
- Validating after a partial mutation breaks AC-6's "no other field applied".
- `wsUpdateTaskRequest` at `:285` and the service request built at `:346` are
  two separate edits; changing only one leaves the WS path silently colourless.
- Special-casing an unchanged colour into a no-op would contradict AC-7 and
  diverge from every other field on this endpoint.

## Parallelism

`sequential`

## Inputs

- Spec sections *API surface / Write*, *Ordering, idempotency, concurrency*,
  *Failure modes*, and *Permissions*.
- `ValidateTaskPriority` at `service_tasks.go:42` and its update-path call at
  `:1928`.
- The `ParentID` / `AssigneeUserID` `*string` omitted/empty convention on the
  same endpoint.

## Results

Pending.
