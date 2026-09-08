---
status: draft
system: office
requirements:
  - REQ-OFFICE-TASK-PROVENANCE-001
  - REQ-OFFICE-TASK-PROVENANCE-002
  - REQ-OFFICE-TASK-PROVENANCE-003
---

# Office Task Provenance System Design

## Purpose and boundaries

Office owns this technical contract because both provenance values are produced
and consumed through Office primitives: `DashboardService.UpdateTaskStatus`
writes them, `office_activity_log` stores the status transition, and the Office
task DTO and detail handler render both.

Adjacent contracts this design uses but does not own:

- The `tasks` table and its migrations, owned by the task system. This design
  adds two columns and constrains the Office migration that rebuilds the table.
- `authn.Identity` and the MCP scope layer, owned by the auth system. This
  design reads identity but deliberately does not derive the creator from it.
- The generic workflow engine's `TaskMoved` handler, which is the pre-existing
  second producer of `old_status` and is left unchanged.

## Requirement mapping

| Requirement | Design section |
| --- | --- |
| `REQ-OFFICE-TASK-PROVENANCE-001` | [Status transition provenance](#status-transition-provenance) |
| `REQ-OFFICE-TASK-PROVENANCE-002` | [Creator identity](#creator-identity) |
| `REQ-OFFICE-TASK-PROVENANCE-003` | [Failure and recovery](#failure-and-recovery) |

## Design decision: persist the creator, do not derive it

The creator could be recorded as a `task_created` activity entry and derived the
way Started and Completed already are. It must not be, on two pieces of measured
evidence:

1. `ListActivityEntriesByTarget` reads `ORDER BY created_at DESC LIMIT 50` and
   is **not filtered by action**. A creation entry is the oldest row a task will
   ever have, so on any task that accumulates 50 activity rows it is evicted
   from the window and a derived creator silently reverts to blank. That fails
   hardest on exactly the long-lived tasks where provenance matters most.
2. The value is wanted in list projections, and deriving it would put a per-row
   activity query behind every task list.

The same reasoning is why the status transition *does* stay in the activity row,
which is where the reader already looks, while the reopen-detection logic stays
off the timeline and on the task's current status.

This mirrors `participant-seat-provenance`, the closest existing neighbour,
which records who created a row on the row itself rather than inferring it.

## Status transition provenance

### Components and responsibilities

| Component | Responsibility |
| --- | --- |
| `DashboardService.UpdateTaskStatus` | Reads `preStatus` before the write; owns the value both writers below need. |
| `logTaskStatusChangeActivity` | Serializes the `details` JSON for the activity row. Gains `old_status`. |
| `publishTaskStatusChanged` | Builds the event payload map. Gains `old_status`. |
| `ListStatusChanges` | Maps `details["old_status"]` onto a timeline event's `From`. Unchanged; it already reads the key. |
| `activity-row.tsx` | Renders a feed entry. Gains a transition form selected on a non-empty `old_status`. |

### Data and contracts

`UpdateTaskStatus` already computes `preStatus` from
`GetTaskExecutionFields(...).State` for `runReactivityForStatus` and
`maybeSupersedeOnRework`, and simply does not forward it. Both writers take it
as a new parameter rather than re-reading it, so all three consumers observe one
value read at one instant.

**Alphabet.** `preStatus` is the persisted `tasks.state` enum; `req.NewStatus`
is the Office canonical status. `dbStateToOfficeStatus` converts the former.
This is the trap the requirement calls out: emitting the raw `preStatus` would
put `COMPLETED` next to `in_progress` in the same JSON object.

**Unknown pre-state.** `dbStateToOfficeStatus("")` returns `"backlog"`, so the
conversion cannot be applied unconditionally. The empty `preStatus` case is
tested before conversion, and the key is omitted from the JSON entirely
(AC-OFFICE-TASK-PROVENANCE-001.4). The `details` writer therefore builds its
JSON with `encoding/json` over a small map or struct rather than the current
`fmt.Sprintf` template, which cannot express an omitted key.

**Ordering against the approval gate.** `applyApprovalGate` mutates `dbState`
and `req.NewStatus` in place, so a `done` request can reach the writers as
`in_review`. `preStatus` is read before the gate runs, so it is unaffected;
AC-OFFICE-TASK-PROVENANCE-001.3 is satisfied by the existing ordering and needs
no new sequencing.

**Event payload.** The payload is a fixed-key `map[string]string`, so a missing
value is the empty string rather than an absent key. Its sole subscriber, the
websocket office-notification bridge, re-broadcasts the payload verbatim using
`subscribe` rather than `subscribeWithout`, so every key is forwarded and
`old_status` reaches browsers on the live-update path without a further change.

### Control flow

```text
UpdateTaskStatus
  read preStatus (tasks.state)        <- before any mutation
  applyApprovalGate (may rewrite NewStatus in place)
  UpdateTaskState                     <- persist
  logTaskStatusChangeActivity(req, preStatus)   -> office_activity_log.details
  publishTaskStatusChanged(req, preStatus)      -> OfficeTaskStatusChanged
                                                -> WS office.task.status
```

The activity row is written before the event is published, which is existing
behavior and load-bearing: it makes the row durable before any broadcast can
trigger a browser refetch. This design preserves that order.

## Creator identity

### Persistence

Two columns on `tasks`, added through the idempotent `ALTER TABLE … ADD COLUMN`
mechanism that already carries `assignee_user_id`:

```sql
created_by_type TEXT NOT NULL DEFAULT ''
created_by_id   TEXT NOT NULL DEFAULT ''
```

**The rebuild hazard.** The Office priority migration recreates `tasks` through
a `CREATE tasks_priority_new` / `INSERT … SELECT` / `DROP` / `RENAME` sequence.
Any column absent from that new shape is silently dropped, so a naive
`ADD COLUMN` can be added and then erased depending on migration order. The
implementation must either register the two statements to run after the rebuild
or carry both columns through the rebuild's column list and its
`INSERT … SELECT`. A migration test asserts presence and value preservation
across the rebuild, following the existing
`migrations_priority_external_id_test.go` pattern
(AC-OFFICE-TASK-PROVENANCE-002.2).

Both columns are write-once, set by the creating INSERT. No update path touches
them, so no interleaving or lost-update concern exists.

### Why the creator is passed in, not read from context

The MCP scope layer mints the **workspace owner's** `authn.Identity` for an
in-session agent stream. A creator derived from request context would therefore
attribute every agent-created task to a human who did not create it. The creator
pair is instead two new fields on the task-creation request, supplied by the
entry point that knows who is acting (AC-OFFICE-TASK-PROVENANCE-002.7).

### Entry-point mapping

This table is the contract referenced by AC-OFFICE-TASK-PROVENANCE-002.8.

| Entry point | `created_by_type` | `created_by_id` |
| --- | --- | --- |
| Office agent action — `runtime.Actions.CreateTask` (root and subtask) | `agent` | `runCtx.AgentID` |
| Agent child-task delegation — `Service.CreateChildTask` (`origin: agent_created`) | `agent` | `parent.AssigneeAgentProfileID`, the parent task's runner |
| `create_task_kandev` over a task-bound MCP stream | `agent` | the agent profile id of the calling session's task, resolved from the bound `taskID`/`sessionID` |
| `create_task_kandev` over a credentialed non-task stream | `user` | `authn.Identity.UserID` |
| Human creation from the web UI | `user` | `authn.Identity.UserID` |
| Routine-generated task | `routine` | the routine id |
| Automation-generated task (`origin: automation_run`, `automation_task`) | `automation` | the automation id |
| Plugin or webhook host write | `plugin` | the plugin id |
| Onboarding and other kandev-internal creation (`origin: onboarding`) | `system` | `""` |

Two rows need their fallback stated, because the identity is not
unconditionally present:

- `CreateChildTask` receives only `parent *models.Task` and a `ChildTaskSpec`.
  It has no session or run context, so `parent.AssigneeAgentProfileID` is the
  only creator identity in scope, and it is empty when the parent has no runner.
- The MCP row's agent profile may not resolve on a stream with no bound task.

If either id resolves empty, the pair is stored as `("", "")` per
AC-OFFICE-TASK-PROVENANCE-002.10 — never as a bare `agent` type with no id, and
never substituted with the *new* task's assignee, which is the agent that will
do the work rather than the one that created it.

**One agent id space.** Office's "agent instance" is a row in `agent_profiles`
(`GetAgentInstance` selects `FROM agent_profiles`), the same id space as the
`agent_profile_id` that `RunnerProjection` surfaces as a task's
`assignee_agent_profile_id`. Every producer in the table writes into that one
space; no second space and no translation is introduced.

### Read surface

`TaskRow` / `TaskSearchResult` gain both columns. The Office task DTO exposes
`createdByType`, `createdById`, and a resolved `createdBy` label.

Label resolution follows `resolveDeciderName`, the existing in-repo pattern for
turning a `(type, id)` actor pair into a display label with a raw-id fallback,
rather than inventing a second one. A creator whose referent has been deleted
renders as its raw id, which keeps a recorded creator distinguishable from an
unrecorded one (AC-OFFICE-TASK-PROVENANCE-002.14 against .15).

On the frontend, `map-office-task.ts` maps `createdBy` from the DTO instead of
hard-coding `""`, and `task-properties.tsx` keeps rendering its placeholder for
the unrecorded case behind the existing `created-by-row` test id.

## Failure and recovery

Every provenance write is best-effort and cannot fail its primary operation
(AC-OFFICE-TASK-PROVENANCE-003.5). This matches the existing guards on the same
writers: `logTaskStatusChangeActivity` returns early when the activity sink is
nil, and `publishTaskStatusChanged` logs a publish error without propagating it.

Degraded outcomes are all "the value is absent", never "the value is wrong":

| Failure | Result |
| --- | --- |
| Pre-status lookup errors | `old_status` key omitted; status change proceeds |
| Activity insert fails | No timeline event; status change proceeds |
| Event publish fails | No live update; refetch still sees the row |
| Creator id unresolvable | Pair stored as `("", "")`; task created |
| Creator referent deleted | Label falls back to the raw id |

## Security

`created_by_*` is descriptive only. No read path filters or authorizes on it
(AC-OFFICE-TASK-PROVENANCE-002.17), so recording it grants no new visibility and
widens no trust boundary. The recorded ids are internal entity ids, not
credentials or personal data beyond the display name already exposed elsewhere
on the same surface.

Recording the synthetic `default-user` while authentication is disabled is
deliberate: it is the actual single user of that installation, so the value
stays correct once authentication is enabled, with no data migration
(AC-OFFICE-TASK-PROVENANCE-002.9).

## Observability

No new metrics. Both values are themselves observability surfaces, and both are
readable through existing channels: the activity feed, the task detail response,
and the `office.task.status` websocket frame. The existing best-effort error
logs on the activity and publish paths remain the signal for a failed
provenance write.

## Verification surfaces

Backend contract behavior (the details JSON, the event payload, the migration,
the entry-point mapping, the best-effort guards) is unit and integration
territory.

Two user-visible surfaces are only reachable through a real create-then-mutate
flow and warrant Playwright coverage under `apps/web/e2e/tests/office/`:

1. A newly created task shows a non-placeholder **Created by** value.
2. A status change renders as a from-to transition in the workspace activity
   feed.

## Verified inputs

Read from the tree at merge-base `18d38c11a`, not assumed.

| Fact | Location |
| --- | --- |
| `From` sourced from `details["old_status"]` | `internal/office/dashboard/service.go` |
| Direct path writes only `new_status` via `fmt.Sprintf` | `internal/office/dashboard/service_tasks.go:663-681` |
| `preStatus` computed and not forwarded to the writers | `internal/office/dashboard/service_tasks.go:611-632` |
| Event payload keys | `internal/office/dashboard/service_tasks.go:968-985` |
| Workflow-move producer writes both keys | `internal/office/service/event_subscribers.go`, `handleTaskMoved` |
| Approval gate rewrites the status in place | `internal/office/dashboard/service_tasks.go:685-705` |
| `dbStateToOfficeStatus("")` returns `"backlog"` | `internal/office/dashboard/service_tasks.go:821-840` |
| Current-status guard clears `CompletedAt` | `internal/office/dashboard/handler.go:488-491` |
| Activity read is `created_at DESC LIMIT 50`, unfiltered by action | `internal/office/repository/sqlite/activity.go:124-141` |
| Activity feed renders `new_status` only | `apps/web/app/office/workspace/activity/activity-row.tsx` |
| No creator column on `tasks` | `internal/task/repository/sqlite/base_schema.go` |
| `TaskSearchResult` has no creator field | `internal/office/repository/sqlite/tasks.go:230-256` |
| `task_created` is a run status, never an activity action | `internal/office/models/enums.go`, `internal/automation/models.go` |
| Office table rebuild drops columns absent from its new shape | `internal/office/repository/sqlite/base_migrations.go` |
| MCP scope mints the workspace owner's identity for agent streams | `internal/mcp/scope/scope.go` |
| Office agent path carries `runCtx.AgentID` | `internal/office/runtime/actions.go` |
| `createdBy` hard-coded empty; rendered as a placeholder | `apps/web/app/office/tasks/[id]/map-office-task.ts:65`, `apps/web/components/task/simple/task-properties.tsx:142` |

## Related decisions

None. This capability adds two provenance fields inside existing Office
contracts and establishes no new architectural boundary.
