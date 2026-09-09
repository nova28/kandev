---
spec: docs/specs/shared-task-color/spec.md
created: 2026-09-09
status: draft
legacy_specs: []
---

# Implementation Plan: Shared, server-owned task colour

## Overview

Add `color` to the task record so a task's sidebar marker becomes task metadata
that any HTTP caller can set, instead of a per-viewer preference living in
`users.settings.sidebar_task_colors`.

The order is forced by data flow. The column and the read DTO come first
(Task 01), because nothing else can be written or rendered until a task can
carry a colour. The write paths come next (Task 02), which is the point where
AC-9 becomes half true: a `curl` `PATCH` can set a colour. Office (Task 03) and
the sidebar read path (Task 04) then fan out over disjoint files. The colour
menu is repointed last among the behaviour changes (Task 05), because until the
field is both writable and rendered, repointing the menu would lose the user's
colour. Documentation amendments (Task 06) and cross-machine E2E evidence
(Task 07) close the package.

The personal store is retained and still read. It is drained per task by the
menu rather than migrated server-side, so no data migration runs and no
multi-user install leaks one person's colours to everyone.

## Scope

### In scope

- A `color` column on `tasks`, applied under the migration key `tasks.color`.
- `color` on `TaskDTO`, serialised without `omitempty`.
- `color` on the task create path, the HTTP `PATCH`, and the WebSocket task
  update action, with `*string` omitted/empty/invalid semantics.
- `color` on `PATCH /office/tasks/:id`.
- Sidebar resolution extended to a third tier (`task.color`) below the
  automatic rule and the viewer's personal manual entry.
- The colour menu writing `task.color` and draining the writer's own personal
  entry for that task.
- The four named amendments to the sidebar task-colours requirement and
  system-design documents.

### Out of scope

Everything listed under *Out of scope* in the spec, notably: a `color` argument
on the MCP task tools, removal of `users.settings.sidebar_task_colors`, bulk
multi-task colour writes, widening the task field to the ten-token automatic
palette, per-workspace colour overrides, colour history or attribution, and
making priority drive the marker by default.

## Corrections to the spec's citations

Verified against `18d38c11a`. The spec is accurate except in three places, and
implementation follows the corrected targets:

1. **The WS update request struct is at `task_ws_handlers.go:285`
   (`wsUpdateTaskRequest`), not `:346`.** Line 346 is where the handler builds
   the `service.UpdateTaskRequest`. Both sites change.
2. **AC-4's cited `v1.CreateTaskRequest` (`pkg/api/v1/task.go:176`) is not on
   the live create path.** `grep` finds no non-test backend use of
   `pkg/api/v1.CreateTaskRequest` or `pkg/api/v1.UpdateTaskRequest`; the plugin
   proto types of the same name are unrelated. `POST /api/v1/tasks` binds
   `httpCreateTaskRequest` (`task_http_handlers.go:742`) and calls
   `service.CreateTaskRequest` (`service_requests.go:72`). AC-4 is satisfied
   against those two structs. The dead `v1` DTOs are left alone.
3. **Office does not share the generic update request.** The spec's table
   implies `dashboard.UpdateTaskRequest` stays in lockstep with the other
   structs, but `applyTaskMutations` (`office/dashboard/handler.go:656`)
   applies each field through a dedicated service method
   (`h.svc.UpdateTaskPriority`), backed by Office's own repository
   (`office/repository/sqlite/tasks.go:870`). AC-18 therefore needs a parallel
   `UpdateTaskColor` at the handler, the `DashboardService` interface
   (`office/dashboard/service.go:77`), the service, and the Office repository.

The spec's open choice on the validator resolves to **call it in place**:
`internal/task` already imports `usermodels`
(`task/handlers/task_http_handlers.go:26`) and `internal/user` imports no
`internal/task` package, so `usermodels.IsValidSidebarTaskColor` is reachable
with no import cycle and no relocation. This satisfies AC-17.

## Technical approach

### Persistence and read contract (Task 01)

- `apps/backend/internal/task/repository/sqlite/base_migrations.go` — add
  `r.migrate.Apply("tasks.color", "ALTER TABLE tasks ADD COLUMN color TEXT NOT NULL DEFAULT ''")`
  beside the `tasks.labels` entry at `:218`.
- `apps/backend/internal/task/models/models.go:860` — `Color string \`json:"color"\``
  next to `Priority`; map it in the API conversion at `:2592`.
- `apps/backend/internal/task/repository/sqlite/task.go` — add `color` to the
  insert, update, and every scan/select column list.
- `apps/backend/pkg/api/v1/task.go:142` — `Color string \`json:"color"\`` beside
  `Priority`, deliberately without `omitempty`.

### Write paths (Task 02)

- `apps/backend/internal/task/service/service_requests.go:127` — `Color *string`
  on `UpdateTaskRequest`; `:72` — `Color string` on `CreateTaskRequest`.
- `apps/backend/internal/task/service/service_tasks.go` — a `ValidateTaskColor`
  wrapper delegating to `usermodels.IsValidSidebarTaskColor` (no second token
  list), invoked in the update path beside the `ValidateTaskPriority` call at
  `:1928` and in the create path near `:767`, before any mutation.
- `apps/backend/internal/task/handlers/task_http_handlers.go:1569` and `:742` —
  `Color *string` / `Color string`, passed through.
- `apps/backend/internal/task/handlers/task_ws_handlers.go:285` and `:346` —
  same field and pass-through.

### Office surface (Task 03)

- `apps/backend/internal/office/dashboard/handler.go:566` — `Color *string`;
  extend `hasAnyField` (`:598`) and `applyTaskMutations` (`:642`).
- `apps/backend/internal/office/dashboard/service.go:77` — `UpdateTaskColor` on
  the interface; implement in `service_tasks.go` beside `UpdateTaskPriority`
  (`:33`) with the same validation.
- `apps/backend/internal/office/repository/sqlite/tasks.go:870` — an
  `UpdateTaskColor` mirroring `UpdateTaskPriority`.

### Sidebar read path (Task 04)

- `apps/web/lib/types/http.ts:423` and
  `apps/web/lib/state/slices/kanban/types.ts:75` — carry `color` beside
  `priority`.
- `apps/web/hooks/use-task-color.ts` — `useTaskColor` returns the tri-state
  resolution: personal entry when the key is *present* (null tombstone yields no
  marker), otherwise `task.color`, otherwise none. The automatic tier above it
  in `resolveTaskItemColor`
  (`apps/web/lib/task-color-presentation.ts`) is unchanged.
- Rule evaluation (`apps/web/lib/sidebar/task-color-rules.ts`,
  `task-color-projection.ts`) is not touched, which is what keeps AC-16 true.

### Colour menu write (Task 05)

- `apps/web/hooks/use-task-color.ts` — `useSetTaskColor` switches from the
  user-settings PATCH to `PATCH /api/v1/tasks/:id` with `color`, then removes
  the writer's own `sidebar_task_colors` entry when one is present.
- `apps/web/components/task/task-switcher-color-menu.tsx` — copy for AC-14 and
  AC-15, in all five catalogs.

## Tests

| AC | Evidence |
| --- | --- |
| AC-1 | `internal/task/repository/sqlite/task_color_test.go` — fresh-create and replay-migration default `''` |
| AC-2 | `internal/task/models/models_test.go` — API conversion emits `"color": ""` |
| AC-3 | `internal/task/handlers/task_http_handlers_test.go`, `task_ws_handlers_test.go` — both accept and persist |
| AC-4 | `internal/task/handlers/task_http_handlers_test.go` — create with and without `color` |
| AC-5 | `internal/task/service/service_task_color_test.go` — omitted leaves value, `""` clears |
| AC-6 | `internal/task/service/service_task_color_test.go` — `"Red"` and `"mauve"` rejected, sibling `title` unapplied |
| AC-7 | `internal/task/service/service_task_color_test.go` — repeat write, `updated_at` advances, `task.updated` published |
| AC-8 | `internal/task/service/service_task_color_test.go` — two writes both succeed, last persists, no revision token |
| AC-11 | `apps/web/hooks/use-task-color.test.tsx` — all four resolution branches incl. null tombstone |
| AC-12 | `apps/web/hooks/use-task-color.test.tsx` — existing personal entry still renders; no server-side copy |
| AC-13 | `apps/web/hooks/use-task-color.test.tsx` — menu write patches the task and drains the personal entry |
| AC-14, AC-15 | `apps/web/components/task/task-switcher-color-menu.test.tsx` |
| AC-16 | `apps/web/lib/sidebar/task-color-rules.test.ts` — rule changes write no task record |
| AC-17 | `internal/task/service/service_task_color_test.go` — validator delegates to `usermodels.IsValidSidebarTaskColor` |
| AC-18 | `internal/office/dashboard/handler_test.go` — omitted/empty/invalid parity with `priority` |
| AC-19 | `internal/task/handlers/task_http_handlers_test.go` — scope rejection, no silent no-op |

## E2E tests

| Flow | ACs | File / project |
| --- | --- | --- |
| `curl` `PATCH` with only `color` changes the rendered marker, no browser involved | AC-9 | `apps/web/e2e/tests/task/sidebar-task-color-sync.spec.ts`, `chromium` |
| A colour set in one context appears in a second browser context without reload, via `task.updated` | AC-10 | same file, `chromium` |
| Same flow at Pixel 5 viewport | AC-9, AC-10 | `apps/web/e2e/tests/task/mobile-sidebar-task-color-sync.spec.ts`, `mobile-chrome` |

`sidebar-automatic-colors.spec.ts` and `mobile-sidebar-automatic-colors.spec.ts`
must keep passing unchanged; they pin AC-16.

## Work orders

- [ ] [Task 01: Persist and serialise the task colour field](task-01-persist-task-color.md)
- [ ] [Task 02: Accept colour on the task create and update paths](task-02-task-color-write-paths.md)
- [ ] [Task 03: Carry colour on the Office task update surface](task-03-office-task-color.md)
- [ ] [Task 04: Resolve the shared task colour in the sidebar](task-04-sidebar-color-resolution.md)
- [ ] [Task 05: Point the colour menu at the task record](task-05-color-menu-writes-task.md)
- [ ] [Task 06: Amend the sidebar task-colours contract](task-06-contract-amendments.md)
- [ ] [Task 07: Cross-machine colour end-to-end evidence](task-07-e2e-cross-machine.md)

### Dependency order

| Wave | Work orders | Gate |
| --- | --- | --- |
| 1 | 01 | A task round-trips a colour through the store and serialises `"color": ""` when unset. |
| 2 | 02 | `curl -X PATCH -d '{"color":"red"}'` persists; an invalid token is a `400` that applies nothing. |
| 3 | 03, 04 (`parallel-safe`) | Office parity with `priority`; the sidebar renders `task.color` for a viewer with no rule and no personal entry. |
| 4 | 05 | Choosing a colour writes the task and the chosen value is what shows. |
| 5 | 06, 07 (`parallel-safe`) | Contract documents match behaviour; cross-context colour delivery proven. |

Tasks 03 and 04 are `parallel-safe`: disjoint files (Go Office packages versus
`apps/web`), no shared schema, migration, generated contract, or lockfile.
Tasks 06 and 07 are `parallel-safe` for the same reason (`docs/` versus
`apps/web/e2e`). All other tasks are `sequential`.

## Verification results

Pending.

## Risks

- **`apps/node_modules` is absent in this worktree.** Every frontend command
  fails with unresolved imports until `(cd apps && pnpm install --frozen-lockfile)`
  runs once. Tasks 04, 05 and 07 each state it.
- **A `NOT NULL DEFAULT ''` `ADD COLUMN` must be proven on replay, not only on
  a fresh database.** An existing install migrates rows that already exist; the
  migration test must cover the replay path, not just `CREATE TABLE`.
- **Omitting `color` from one of the four scan/select column lists in
  `task.go` reads a zero value and silently blanks colours** while every write
  test still passes. Pin a read-after-write through the repository.
- **`omitempty` on `TaskDTO.Color` would break AC-2** and make "cleared" and
  "old server" indistinguishable. The field must match `Priority`'s tag exactly.
- **Forking a second seven-token list in the task package would pass every test
  and violate AC-17.** Delegate to `usermodels.IsValidSidebarTaskColor`.
- **Validating after a partial mutation breaks AC-6.** The colour check must sit
  with the priority check, before any field is applied.
- **Repointing the menu (Task 05) before Task 04 renders `task.color` would make
  every colour appear to vanish on write.** The wave order is load-bearing, not
  cosmetic.
- **Draining the personal entry is what makes AC-13 observable.** If the task
  `PATCH` succeeds and the personal clear does not, the stale personal entry
  still wins for that viewer and the menu looks broken; the spec's failure table
  requires the save-error toast plus a retry on next settings load.
- **Five locale catalogs gate the build.** New menu copy needs `pt-pt`, `zh-cn`,
  `zh-hk`, `zh-tw`; use `pnpm run i18n:zh-hant` for the Traditional pair. No
  em dashes in user-facing copy.
- **AC-9 is the criterion the card exists for.** An E2E that drives the menu
  instead of issuing a serverside request would not prove it; the test must
  patch over HTTP with no browser in the write path.
