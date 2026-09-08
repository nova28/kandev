---
id: "03-creator-columns"
title: "Add task creator columns and carry them through the Office rebuild"
status: pending
wave: 1
depends_on: []
plan: "plan.md"
requirements:
  - REQ-OFFICE-TASK-PROVENANCE-002
  - REQ-OFFICE-TASK-PROVENANCE-003
acceptance_criteria:
  - AC-OFFICE-TASK-PROVENANCE-002.1
  - AC-OFFICE-TASK-PROVENANCE-002.2
  - AC-OFFICE-TASK-PROVENANCE-002.3
  - AC-OFFICE-TASK-PROVENANCE-002.6
  - AC-OFFICE-TASK-PROVENANCE-003.6
system_design:
  - ../../specs/office/system-design/task-provenance-01.md
---

# Task 03: Add Task Creator Columns and Carry Them Through the Office Rebuild

## Summary

Add `created_by_type` and `created_by_id` to the `tasks` table through the
idempotent `ALTER TABLE … ADD COLUMN` mechanism, and carry both through the
Office priority rebuild so they survive on installs that still run it. Storage
only: no entry point populates them yet.

## In scope

- Add the migration test before the schema change.
- Add both columns as `TEXT NOT NULL DEFAULT ''`, following
  `team_access_migration.go:32`.
- Add both to the Office rebuild's `CREATE`, its `INSERT` column list, and its
  `SELECT` with `COALESCE`, following the `assignee_user_id` precedent.
- Carry both onto the task model and the base row projection.

## Out of scope

- Populating the columns, which is Task 04.
- The Office task DTO, label resolution, and any UI, which is Task 05.
- Backfilling a creator for existing rows (AC-002.6) — every pre-existing row
  keeps the empty pair.

## Acceptance

- Both columns exist with an empty-string default, and a row created before the
  change reads back as `("", "")` rather than null.
- A database that goes through the Office priority rebuild retains both columns
  and their values, asserted by a test following
  `migrations_priority_assignee_test.go`.
- The migration is idempotent across repeated boots.

## Verification

```bash
cd apps/backend
go test ./internal/office/repository/sqlite/ -run 'TestMigrationsPriority'
go test ./internal/office/repository/sqlite/... ./internal/task/repository/sqlite/...
make -C . fmt
make -C . test
make -C . lint
```

Persistence changes additionally run the SQL guard and the fresh/replay
conformance suites named in `CLAUDE.md`.

## Files likely touched

- `apps/backend/internal/task/repository/sqlite/` (the `ADD COLUMN` migration)
- `apps/backend/internal/office/repository/sqlite/base_migrations.go`
- `apps/backend/internal/office/repository/sqlite/migrations_priority_creator_test.go`
- `apps/backend/internal/task/models/models.go`

## Dependencies

None.

## Risks

- **This is the only step that can lose data silently.** The Office rebuild
  drops any column absent from its new shape, with no error. The comment at
  `base_migrations.go:634-644` states that task-side `ADD COLUMN` migrations run
  *before* the recreate, and lists `archived_by_cascade_id`, `external_id`, and
  `assignee_user_id` as columns carried through for exactly this reason. Both
  new columns must appear in all three places — `CREATE`, `INSERT` list, and
  `SELECT` — not just the first.
- `COALESCE` both columns to `''` in the `SELECT`, matching `assignee_user_id`.
  `external_id` is deliberately not coalesced because NULL is meaningful there;
  that exception does not apply to these columns.

## Parallelism

`parallel-safe` with Task 01, which touches no schema and no shared file.

## Inputs

- AC-OFFICE-TASK-PROVENANCE-002.1 through .3, .6, and AC-003.6.
- System design, "Persistence".
- `base_migrations.go:601-672` and `migrations_priority_assignee_test.go`.

## Results

Pending.
