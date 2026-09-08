---
created: 2026-09-09
status: draft
requirements:
  - REQ-OFFICE-TASK-PROVENANCE-001
  - REQ-OFFICE-TASK-PROVENANCE-002
  - REQ-OFFICE-TASK-PROVENANCE-003
system_design:
  - ../../specs/office/system-design/task-provenance-01.md
legacy_specs: []
---

# Implementation Plan: Office Task Provenance

## Overview

Give an Office task two provenance values it does not currently have: what a
status change moved *from*, and who created the task. The two are independent
in the code and are sequenced as two chains that can land separately.

Chain A (status transition) is small and self-contained: one value already read
in `UpdateTaskStatus` is threaded to two writers, then rendered. Chain B
(creator identity) is a schema change, so it is ordered storage before writers
before readers — a creator column that no entry point populates is harmless,
but a writer targeting a column that the Office table rebuild silently drops is
not.

Chain A ships first because it carries no migration risk and closes the only
gap with an existing user-visible symptom.

## Scope

### In scope

- `old_status` in the `task_status_changed` activity details and in the
  `OfficeTaskStatusChanged` event payload, from the direct status-update path.
- Transition rendering in the workspace activity feed, in five locales.
- `created_by_type` / `created_by_id` on `tasks`, carried through the Office
  priority rebuild.
- Creator population at every production task-creation entry point.
- Creator projection, label resolution, DTO fields, and sidebar rendering.

### Out of scope

Carried from the requirement's exclusions, restated here because they are the
things a builder is most likely to add unasked:

- Reviving a `from`-based Completed-timestamp clearing rule. The current-status
  guard at `handler.go:488-491` already owns that field.
- Widening or filtering the 50-row activity read window.
- Rendering the `timeline` response array in the web UI.
- A `task_created` activity entry.
- Backfilling a creator for existing rows.
- Any authorization, filtering, or "created by me" view keyed on the creator.

## Technical approach

### Chain A — status transition provenance

`UpdateTaskStatus` (`service_tasks.go:611-632`) already reads `preStatus` from
`GetTaskExecutionFields(...).State` before the approval gate runs, and passes it
to `runReactivityForStatus` and `maybeSupersedeOnRework`. Both provenance
writers take it as a new parameter rather than re-reading it, so all consumers
observe one value read at one instant, and AC-001.3 is satisfied by the existing
ordering with no new sequencing.

Two mechanical constraints decide the implementation:

- **Alphabet.** `preStatus` is the `tasks.state` enum; `req.NewStatus` is the
  Office canonical status. `dbStateToOfficeStatus` converts the former
  (AC-001.2).
- **Omitted key.** `dbStateToOfficeStatus("")` returns `"backlog"`
  (`service_tasks.go:835`), so the conversion cannot run unconditionally — an
  unreadable pre-state would fabricate a transition out of Backlog. The empty
  case is tested *before* conversion and the key is dropped (AC-001.4). Today's
  writer builds its JSON with `fmt.Sprintf` (`service_tasks.go:679-681`), which
  cannot express an omitted key, so it moves to `encoding/json` over a map.

The event payload is a fixed-key `map[string]string`, where absence is not
expressible; the unknown case is the empty string there (AC-001.5). Its only
subscriber re-broadcasts the payload verbatim with `subscribe`, so no separate
websocket change is needed.

The feed adds one key for the whole clause, matching the rationale already
written into `activity-row.tsx:107-109` for the existing key: two fragments
would freeze the English order.

### Chain B — creator identity

**Storage.** Two columns via the idempotent `ALTER TABLE … ADD COLUMN`
mechanism that carries `assignee_user_id`
(`internal/task/repository/sqlite/team_access_migration.go:32`).

The Office priority rebuild is the hazard, and the codebase has already
answered it. `base_migrations.go:634-644` states that task-side `ADD COLUMN`
migrations run **before** the Office recreate, and that any column omitted from
the new shape is dropped; `archived_by_cascade_id`, `external_id`, and
`assignee_user_id` are each carried through the `CREATE`, the `INSERT` column
list, and the `SELECT` with `COALESCE`. This plan follows that established
precedent rather than the requirement's alternative of reordering the
statements, because the precedent is in-tree, tested, and commented.

**Writers.** The creator pair is two new fields on
`taskservice.CreateTaskRequest` (`internal/task/service/service_requests.go:72`),
supplied by the caller. It is deliberately *not* derived from context inside the
service: `internal/mcp/scope/scope.go` mints the workspace owner's identity for
an in-session agent stream, so a context-derived creator would attribute
agent-created tasks to a human (AC-002.7). There are roughly ten production call
sites (`backendapp/adapters_office.go`, `backendapp/orchestrator.go`,
`backendapp/canvas_routes.go`, `backendapp/services.go`,
`task/handlers/task_http_handlers.go`, `task/handlers/task_ws_handlers.go`,
`task/service/service_child_task.go`, `plugins/host_write.go`); each maps to a
row of the system design's entry-point table. A call site that supplies nothing
stores `("", "")`, which is the correct value, not a defect.

**Readers.** `TaskRow` / `TaskSearchResult`
(`internal/office/repository/sqlite/tasks.go:230-256`) gain both columns; the
DTO gains `createdByType`, `createdById`, and a resolved `createdBy`. Label
resolution reuses `resolveDeciderName` (`internal/office/dashboard/decisions.go:610`)
rather than introducing a second actor-label resolver.

## Tests

| Acceptance criteria | Evidence |
| --- | --- |
| AC-001.1, .2, .3, .7 | `internal/office/dashboard/status_change_activity_test.go` — extend the existing file: details carry both keys, canonical alphabet, gate-redirect case, same-status case |
| AC-001.4 | `status_change_activity_test.go` — unreadable pre-state omits the key, and does not emit `backlog` |
| AC-001.5 | `internal/office/dashboard/status_change_activity_test.go` — published payload carries `old_status`, empty when unknown |
| AC-001.6 | `internal/office/service/event_subscribers_test.go` — workflow-move producer unchanged |
| AC-001.8 | `internal/office/dashboard` handler test — `timeline[].from` populated for direct-path entries |
| AC-001.9 | `apps/web/app/office/workspace/activity/activity-row.test.tsx` — transition form, fallback form |
| AC-001.10, AC-003.1, .2, .3, .4 | `internal/office/dashboard/completed_at_reopen_test.go` and the existing dashboard suite stay green unmodified |
| AC-002.1, .2, .3 | `internal/office/repository/sqlite/migrations_priority_creator_test.go` — new, following `migrations_priority_assignee_test.go` |
| AC-002.4, .5, .6, .10, .11 | `internal/task/service` tests — write-once, half-identity collapse, `external_id` dedupe keeps the first creator |
| AC-002.7, .8, .9 | `internal/backendapp` and `internal/office/runtime` tests — per-entry-point mapping, agent path uses `runCtx.AgentID`, disabled-auth default user |
| AC-002.12, .13, .14 | `internal/office/dashboard` DTO tests — three fields, label resolution, raw-id fallback |
| AC-002.15, .16 | `apps/web/app/office/tasks/[id]/map-office-task.test.ts` — replace the existing "always maps createdBy to an empty string" case (line 54) with populated and unrecorded cases |
| AC-002.17, AC-003.5, .6 | Existing task list/detail authorization tests stay green; best-effort guards asserted alongside AC-001.4 |

## E2E tests

Both flows are only reachable through a real create-then-mutate sequence, so
each is owned by the work order that ships its surface rather than by a trailing
test task. Project: `chromium`.

| Flow | ACs | File |
| --- | --- | --- |
| A status change renders as a from-to transition in the workspace activity feed | AC-001.9 | `apps/web/e2e/tests/office/activity-page.spec.ts` |
| A newly created task shows a non-placeholder Created by value | AC-002.15, AC-002.16 | `apps/web/e2e/tests/office/task-content-editing.spec.ts` or a sibling office task-detail spec |

## Work orders

Wave 1 — no dependencies, disjoint files:

- [ ] [Task 01: Record the prior status on the direct status-update path](task-01-record-old-status.md)
- [ ] [Task 03: Add task creator columns and carry them through the Office rebuild](task-03-creator-columns.md)

Wave 2:

- [ ] [Task 02: Render the status transition in the workspace activity feed](task-02-render-status-transition.md)
- [ ] [Task 04: Populate the creator pair at every task-creation entry point](task-04-populate-creator.md)

Wave 3:

- [ ] [Task 05: Project, resolve, and render the task creator](task-05-render-creator.md)

## Verification results

Pending.

## Risks

- **The Office table rebuild silently drops columns.** This is the one change
  that can lose data rather than merely omit it. A column added by an
  `ALTER TABLE` that runs before the rebuild, and absent from the rebuild's new
  shape, disappears on any install that still needs the rebuild — with no error.
  Task 03 carries both columns through the `CREATE`, `INSERT`, and `SELECT`, and
  its migration test is the guard. That test must not be weakened.
- **`dbStateToOfficeStatus("")` returns `"backlog"`.** The single most likely
  implementation error in Chain A is converting the pre-state unconditionally,
  which turns "we could not read it" into a confident, wrong claim that the task
  left Backlog. The omitted-key test is the guard.
- **Creator attribution can be confidently wrong.** Deriving the creator from
  request context looks correct and passes casual review, but the MCP scope
  layer mints the workspace owner's identity for agent streams, so every
  agent-created task would be attributed to a human. The entry-point table is
  the contract; a reviewer should check call sites against it rather than trust
  a single service-level default.
- **Five-language copy gates the build.** Task 02 adds a user-facing key, so
  `pnpm run i18n:check` fails until `pt-pt`, `zh-cn`, `zh-hk`, and `zh-tw` are
  present. Use `pnpm run i18n:zh-hant` for the Traditional pair.
- **This worktree has no `apps/node_modules`.** Every frontend command needs
  `pnpm install --frozen-lockfile` from `apps/` once, or it fails with a
  resolution error rather than a useful message.
