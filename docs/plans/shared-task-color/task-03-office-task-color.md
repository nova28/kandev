---
id: "03-office-task-color"
title: "Carry colour on the Office task update surface"
status: pending
wave: 3
depends_on: ["02-task-color-write-paths"]
plan: "plan.md"
spec: "../../specs/shared-task-color/spec.md"
acceptance_criteria:
  - AC-18
---

# Task 03: Carry colour on the Office task update surface

## Summary

Give `PATCH /office/tasks/:id` a `color` field with the same semantics as the
task endpoint, so that every request path which today carries `priority` also
carries colour. Office applies fields through dedicated service methods, so this
mirrors `UpdateTaskPriority` rather than reusing the shared update request.

## In scope

- `Color *string` on `dashboard.UpdateTaskRequest`, plus its `hasAnyField` and
  `applyTaskMutations` branches.
- `UpdateTaskColor` on the `DashboardService` interface and its implementation,
  validated with the same palette check.
- `UpdateTaskColor` on the Office SQLite repository, mirroring
  `UpdateTaskPriority`.

## Out of scope

- The task HTTP and WebSocket endpoints (Task 02).
- Every frontend surface.

## Acceptance

- Omitted leaves the colour unchanged, `""` clears it, an invalid token is
  rejected, matching the `priority` field on the same request.
- A colour set through the Office endpoint is visible on the task API.

## Verification

```bash
(cd apps/backend && go test ./internal/office/...)
make -C apps/backend lint
```

## Files likely touched

- `apps/backend/internal/office/dashboard/handler.go`
- `apps/backend/internal/office/dashboard/service.go`
- `apps/backend/internal/office/dashboard/service_tasks.go`
- `apps/backend/internal/office/repository/sqlite/tasks.go`
- `apps/backend/internal/office/dashboard/handler_test.go`

## Dependencies

Task 02 — reuses the validation introduced there.

## Risks

- Adding `Color` to the request struct without extending `hasAnyField` makes a
  colour-only request a silent no-op.
- Office writes through its own repository; changing only the dashboard service
  leaves nothing persisted.

## Parallelism

`parallel-safe` with Task 04 — disjoint files, no shared schema, migration,
generated contract, or package configuration.

## Inputs

- Spec section *API surface / Write* and AC-18.
- `UpdateTaskPriority` at `office/dashboard/service_tasks.go:33`,
  `office/dashboard/service.go:77`, and
  `office/repository/sqlite/tasks.go:870`.
- `applyTaskMutations` at `office/dashboard/handler.go:642`.

## Results

Pending.
