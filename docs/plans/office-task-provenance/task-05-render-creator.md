---
id: "05-render-creator"
title: "Project, resolve, and render the task creator"
status: pending
wave: 3
depends_on: ["04-populate-creator"]
plan: "plan.md"
requirements:
  - REQ-OFFICE-TASK-PROVENANCE-002
acceptance_criteria:
  - AC-OFFICE-TASK-PROVENANCE-002.12
  - AC-OFFICE-TASK-PROVENANCE-002.13
  - AC-OFFICE-TASK-PROVENANCE-002.14
  - AC-OFFICE-TASK-PROVENANCE-002.15
  - AC-OFFICE-TASK-PROVENANCE-002.16
  - AC-OFFICE-TASK-PROVENANCE-002.17
system_design:
  - ../../specs/office/system-design/task-provenance-01.md
---

# Task 05: Project, Resolve, and Render the Task Creator

## Summary

Carry the creator columns into the Office task projection and DTO, resolve a
human-readable label with a raw-id fallback, and render it in the task detail
sidebar's **Created by** row in place of the hard-coded empty string.

## In scope

- Add the resolver, mapper, and E2E tests before the production change.
- Add both columns to `TaskRow` / `TaskSearchResult`.
- Expose `createdByType`, `createdById`, and a resolved `createdBy` on the DTO.
- Resolve the label through `resolveDeciderName`, including its raw-id fallback.
- Map `createdBy` from the DTO in `map-office-task.ts` and replace the unit test
  that asserts the hard-coded empty string.

## Out of scope

- Writing the creator, which is Task 04.
- Hiding the **Created by** row when no creator is recorded — the row keeps its
  placeholder (AC-002.15).
- Any filtering, authorization, or "created by me" view (AC-002.17).
- A localized "System" label beyond the single key AC-002.13 requires.

## Acceptance

- A task created after Task 04 shows a resolved creator name in the sidebar; a
  pre-existing task shows the existing `--` placeholder and the row stays
  visible.
- A creator whose referent has been deleted renders as its raw id, not as blank,
  keeping a recorded creator distinguishable from an unrecorded one.
- No task list or detail read path filters or authorizes on the creator, and the
  existing authorization tests stay green.

## Verification

`apps/node_modules` is absent in this worktree, so install once before the first
package command.

```bash
cd apps/backend
go test ./internal/office/dashboard/... ./internal/office/repository/sqlite/...
make -C . fmt
make -C . test
make -C . lint

cd ../
pnpm install --frozen-lockfile
pnpm --filter @kandev/web test -- map-office-task
pnpm --filter @kandev/web lint

cd web
pnpm run i18n:check
pnpm e2e:run tests/office/task-content-editing.spec.ts -- --project=chromium
```

## Files likely touched

- `apps/backend/internal/office/repository/sqlite/tasks.go`
- `apps/backend/internal/office/dashboard/{handler.go,decisions.go}`
- `apps/web/app/office/tasks/[id]/map-office-task.ts`
- `apps/web/app/office/tasks/[id]/map-office-task.test.ts`
- `apps/web/src/locales/{en,pt-pt,zh-cn,zh-hk,zh-tw}/task.json`
- `apps/web/e2e/tests/office/task-content-editing.spec.ts`

## Dependencies

Task 04 — there is nothing to render until an entry point writes a creator.

## Risks

- `map-office-task.test.ts:54` currently asserts `createdBy` is *always* the
  empty string. That test must be replaced, not deleted, or the mapper loses its
  only coverage.
- Reuse `resolveDeciderName` (`decisions.go:610`) rather than writing a second
  actor-label resolver; its raw-id fallback is what AC-002.14 requires.
- A "System" creator label is user-facing copy and needs all five locales.

## Parallelism

`sequential`

## Inputs

- AC-OFFICE-TASK-PROVENANCE-002.12 through .17.
- System design, "Read surface".
- `tasks.go:230-256`, `decisions.go:610`, `map-office-task.ts:65`,
  `task-properties.tsx:142`.

## Results

Pending.
