---
id: "01-persist-task-color"
title: "Persist and serialise the task colour field"
status: pending
wave: 1
depends_on: []
plan: "plan.md"
spec: "../../specs/shared-task-color/spec.md"
acceptance_criteria:
  - AC-1
  - AC-2
---

# Task 01: Persist and serialise the task colour field

## Summary

Give the task record a `color` column and carry it through the model, the SQLite
repository, and `TaskDTO`. After this work order a task can hold a colour and
every endpoint that returns a task reports it, but nothing can set one yet.

## In scope

- The `tasks.color` migration, mirroring the `tasks.labels` house style.
- `models.Task.Color` and its API conversion.
- `color` in every insert, update, and scan/select column list in the task
  SQLite repository.
- `TaskDTO.Color`, tagged `json:"color"` without `omitempty`.

## Out of scope

- Any request struct, validation, or write path (Task 02).
- Office (Task 03) and all frontend work (Tasks 04, 05).

## Acceptance

- A fresh database and a replayed migration both leave `color` as `''` on every
  pre-existing and newly created row.
- A task with no colour serialises as `"color": ""`, never as an absent field.
- A colour written directly through the repository is returned by a subsequent
  read.

## Verification

```bash
(cd apps/backend && go test ./internal/task/... ./pkg/api/...)
make -C apps/backend lint
```

## Files likely touched

- `apps/backend/internal/task/repository/sqlite/base_migrations.go`
- `apps/backend/internal/task/repository/sqlite/task.go`
- `apps/backend/internal/task/models/models.go`
- `apps/backend/pkg/api/v1/task.go`
- `apps/backend/internal/task/repository/sqlite/task_color_test.go` (new)

## Dependencies

None.

## Risks

- Missing one of the scan/select column lists in `task.go` blanks colours on
  read while every write test still passes; prove a read-after-write.
- Adding `omitempty` to `TaskDTO.Color` breaks AC-2 and hides the difference
  between a cleared colour and an older server.
- Testing the migration only against a fresh schema misses the `ADD COLUMN`
  path that existing installs actually take.

## Parallelism

`sequential`

## Inputs

- Spec sections *Data model* and *API surface / Read*.
- `r.migrate.Apply("tasks.labels", ...)` at `base_migrations.go:218` as the
  migration precedent.
- The adjacent `Priority` field on `models.Task` and `TaskDTO` as the tag
  precedent.

## Results

Pending.
