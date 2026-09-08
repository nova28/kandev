---
status: draft
system: office
created: 2026-09-08
owners:
  - kandev
---

# Office Task Provenance Requirements

## Overview

Two provenance gaps on the Office task detail surface, both requiring a contract
or schema change. They are specified together because they share one read
surface (the task detail sidebar and the workspace activity feed) and one design
question — *persist the fact, or derive it from the activity timeline* — which
the paired system design answers once and applies to both.

- **Status transitions.** The Office direct status-update path records only
  *what a task changed to*, never *what it changed from*. Every status-change
  activity entry written on the path Office users actually take is
  half-recorded.
- **Creator identity.** The task detail sidebar's **Created by** row has no
  backing data at any layer and is permanently blank.

The Office system owns this contract because both values are read through Office
primitives: the `office_activity_log`, the Office task DTO, and the Office task
detail handler. The `tasks` table is an adjacent contract owned by the
[task system](../../tasks/README.md) that this capability extends but does not
otherwise govern.

## Terminology

- **Direct status-update path:** `DashboardService.UpdateTaskStatus`, reached by
  the Office status picker, `PATCH /api/v1/office/tasks/:id`, and every
  agent/MCP status write through `UpdateTaskStatusAsAgent`.
- **Workflow-move path:** the generic workflow engine's `TaskMoved` handler,
  reached by Kanban-style workflow moves. A second, pre-existing producer of the
  same activity action.
- **Office canonical status:** the lowercase status vocabulary (`done`,
  `in_progress`, `in_review`, `todo`, `blocked`, `cancelled`, `backlog`) used on
  the wire, as distinct from the persisted `tasks.state` enum (`COMPLETED`,
  `IN_PROGRESS`, …).
- **Creator pair:** the `(created_by_type, created_by_id)` tuple identifying who
  created a task, as distinct from `tasks.origin`, which records the *kind* of
  creation without an identity.
- **Unrecorded creator:** a creator pair of `("", "")`. Distinct from a creator
  whose referent has since been deleted, which remains recorded.

## Requirements

### REQ-OFFICE-TASK-PROVENANCE-001: Record the prior status on a status change

**Intent:** A status-change record that names only the destination cannot be
rendered as a transition and cannot support any reader that needs to know what
the task left. Make the direct path record the same transition the workflow-move
path already records.

**User story:** As a user reading a workspace activity feed, I want a status
change to show what the task moved from as well as what it moved to, so that I
can follow a task's history without reconstructing it.

The two values are computed in different alphabets, which is the one trap in
this requirement: the requested status is already Office canonical, while the
pre-update state is the persisted `tasks.state` enum and must be converted.

#### Acceptance criteria

- **AC-OFFICE-TASK-PROVENANCE-001.1:** When the direct status-update path
  records a `task_status_changed` activity entry, the system shall include an
  `old_status` key in the entry's `details` JSON alongside the existing
  `new_status` key.
- **AC-OFFICE-TASK-PROVENANCE-001.2:** The system shall express `old_status` in
  the same vocabulary as `new_status` on the same entry, the Office canonical
  status, by converting the pre-update `tasks.state` through the existing
  state-to-status conversion. A producer shall not emit one key in the
  persisted-state alphabet and the other in the canonical alphabet.
- **AC-OFFICE-TASK-PROVENANCE-001.3:** The system shall record as `old_status`
  the task's state as read *before* the update was persisted, unaffected by the
  approval gate's in-place redirect of the requested status. When a `done`
  request is redirected to `in_review`, the entry shall record
  `new_status: "in_review"` and the genuine pre-update status as `old_status`.
- **AC-OFFICE-TASK-PROVENANCE-001.4:** If the pre-update state cannot be
  determined, because the task row is missing or the lookup errors, then the
  system shall omit the `old_status` key entirely rather than emit an empty or
  defaulted value. Converting an unknown pre-state would fabricate a transition
  out of Backlog that never happened, because the conversion maps the empty
  string to `backlog`. An absent key is how "not observed" is expressed, and
  readers already yield the empty string for an absent key.
- **AC-OFFICE-TASK-PROVENANCE-001.5:** When the direct status-update path
  publishes the Office task-status-changed event, the payload shall carry an
  `old_status` field holding the same value, under the same rules, as the
  activity entry for that same update. When the value is unknown per
  AC-OFFICE-TASK-PROVENANCE-001.4, the field shall be the empty string, because
  the payload is a fixed-key map in which absence is not expressible. The field
  is not write-only: the event's sole subscriber re-broadcasts the payload
  verbatim to browsers, so `old_status` shall reach the client on the
  live-update path as well as the refetch path.
- **AC-OFFICE-TASK-PROVENANCE-001.6:** The system shall leave the
  workflow-move path's existing `old_status` behavior unchanged. This capability
  adds a second producer of the key; it does not redefine the first.
- **AC-OFFICE-TASK-PROVENANCE-001.7:** The direct status-update path shall
  continue to record an activity entry when the requested status equals the
  current status, as it does today, and that entry shall carry `old_status`
  equal to `new_status`. This is stated rather than left silent because the
  workflow-move path suppresses same-status moves and the direct path does not;
  a builder holding both in view would otherwise have to invent which governs.
- **AC-OFFICE-TASK-PROVENANCE-001.8:** When a task detail response is built,
  every timeline event derived from an activity entry carrying `old_status`
  shall expose it as the event's `from` field, for entries written by either
  producer.
- **AC-OFFICE-TASK-PROVENANCE-001.9:** When the workspace activity feed renders
  a `task_status_changed` entry whose details carry a non-empty `old_status`, it
  shall render the transition using both statuses, each resolved through the
  existing shared status-label map. If `old_status` is absent or empty, then it
  shall render today's single-status phrasing unchanged. Each form shall be a
  single localization key, so that translation is not order-frozen to English.
- **AC-OFFICE-TASK-PROVENANCE-001.10:** The system shall change no
  status-change behavior other than the recorded details. The persisted state,
  the approval gate, the reactivity pipeline, rework supersession, and the
  sidebar's Started and Completed derivation shall all behave as they do today.

### REQ-OFFICE-TASK-PROVENANCE-002: Record and display who created a task

**Intent:** The **Created by** sidebar row is wired end to end except that no
layer holds a creator. Give a task a creator identity written once at creation,
and render it.

**User story:** As a user looking at an Office task, I want to see who or what
created it, so that I can tell an agent-delegated task from one a person filed.

#### Storage

- **AC-OFFICE-TASK-PROVENANCE-002.1:** The `tasks` table shall carry two creator
  columns, `created_by_type TEXT NOT NULL DEFAULT ''` and `created_by_id TEXT
  NOT NULL DEFAULT ''`, added through the same idempotent
  `ALTER TABLE … ADD COLUMN` mechanism that already carries `assignee_user_id`.
- **AC-OFFICE-TASK-PROVENANCE-002.2:** The columns shall survive the Office
  priority-rebuild migration, which recreates the whole `tasks` table and
  silently drops any column absent from its new shape. This shall be satisfied
  either by ordering the two `ADD COLUMN` statements to run after the rebuild,
  or by carrying both columns through the rebuild's column list and its
  `INSERT … SELECT`. A migration test shall assert the columns are present and
  their values preserved on a database that goes through the rebuild.
- **AC-OFFICE-TASK-PROVENANCE-002.3:** `created_by_type` shall be one of `user`,
  `agent`, `routine`, `automation`, `plugin`, `system`, or the empty string. The
  empty string means the creator was not recorded and is the value for every row
  created before this capability.
- **AC-OFFICE-TASK-PROVENANCE-002.4:** `created_by_id` shall identify the
  creator within its type: a user id for `user`, an agent profile id for
  `agent`, a routine id for `routine`, an automation id for `automation`, a
  plugin id for `plugin`, and the empty string for `system`. There shall be
  exactly one agent id space, the `agent_profiles` id space that Office's agent
  instances and a task's assignee agent profile already share. A builder shall
  not introduce a second agent id space or a translation between them.
- **AC-OFFICE-TASK-PROVENANCE-002.5:** The system shall write the creator pair
  once, by the INSERT that creates the task, and shall treat it as immutable
  thereafter. No update, move, reassign, archive, or reopen path shall modify
  either column.
- **AC-OFFICE-TASK-PROVENANCE-002.6:** The system shall not backfill a creator
  for rows that predate this capability, and shall not infer retroactive
  attribution from `tasks.origin`, from activity rows, or from the workspace
  owner.

#### Determining the creator

- **AC-OFFICE-TASK-PROVENANCE-002.7:** The creator pair shall be supplied
  explicitly by the entry point that creates the task, as two new fields on the
  task-creation request, and shall not be inferred inside the task service from
  the request context identity. The reason is measured: the MCP scope layer
  mints the *workspace owner's* identity for an in-session agent stream, so a
  context-derived creator would attribute agent-created tasks to a human who did
  not create them.
- **AC-OFFICE-TASK-PROVENANCE-002.8:** Each creation entry point shall map to
  the creator pair named for it in the paired system design's entry-point table.
  An entry point not named there, or one that supplies nothing, falls under
  AC-OFFICE-TASK-PROVENANCE-002.10.
- **AC-OFFICE-TASK-PROVENANCE-002.9:** While authentication is disabled, the
  synthetic identity's default user shall be recorded as a normal `user`
  creator. It is the actual single user of that installation, and recording it
  keeps the value correct after authentication is later enabled without a data
  migration.
- **AC-OFFICE-TASK-PROVENANCE-002.10:** If an entry point supplies a
  `created_by_type` with an empty `created_by_id` for any type other than
  `system`, or supplies an id with no type, then the system shall store the pair
  as `("", "")` rather than persist a half-identity. A partially-known creator
  is recorded as unknown.
- **AC-OFFICE-TASK-PROVENANCE-002.11:** If a creation request is deduplicated by
  `external_id` and returns the pre-existing task, then the creator pair on that
  task shall remain the first creator's and shall not be overwritten by the
  second caller's. Creator provenance follows the immutability of
  AC-OFFICE-TASK-PROVENANCE-002.5, not last-writer-wins.

#### Read surface

- **AC-OFFICE-TASK-PROVENANCE-002.12:** The Office task row projection shall
  carry both creator columns, and the Office task DTO shall expose three fields:
  the raw `createdByType` and `createdById`, and a resolved human-readable
  `createdBy` label.
- **AC-OFFICE-TASK-PROVENANCE-002.13:** When the `createdBy` label is resolved,
  the system shall use, for `user`, the user's display name falling back to
  their email; for `agent`, the agent instance's name; for `routine`,
  `automation` and `plugin`, that entity's name; and for `system`, a localized
  "System" label.
- **AC-OFFICE-TASK-PROVENANCE-002.14:** If the referenced user, agent, routine,
  automation, or plugin cannot be resolved, because it was deleted or is no
  longer readable, then the system shall fall back to the raw `created_by_id`
  rather than return an empty label, matching the documented fallback of the
  existing decider-name resolver. A creator that was recorded shall never render
  as unknown merely because its referent is gone.
- **AC-OFFICE-TASK-PROVENANCE-002.15:** While `created_by_type` is the empty
  string, the DTO's `createdBy` shall be the empty string and the sidebar's
  **Created by** row shall render its existing placeholder. The row shall not be
  hidden: a task with no recorded creator differs from a task with no such
  concept, and the row is the only place that difference is legible.
- **AC-OFFICE-TASK-PROVENANCE-002.16:** The Office task mapper shall map
  `createdBy` from the DTO instead of hard-coding the empty string, and its unit
  test asserting the hard-coded empty string shall be replaced by tests covering
  a populated creator and an unrecorded one.
- **AC-OFFICE-TASK-PROVENANCE-002.17:** Recording and reading a creator shall
  not change task visibility, authorization, or scoping. The creator pair is
  descriptive only, and no read path shall filter or authorize on it in this
  capability.

### REQ-OFFICE-TASK-PROVENANCE-003: Keep provenance writes subordinate to the operation they describe

**Intent:** Both records above are descriptive. Neither may acquire the power to
fail, reorder, or serialize the operation it describes. These criteria are
stated so that no builder has to invent them.

**User story:** As a user, I want a status change or a task creation to succeed
even when its provenance record cannot be written, so that bookkeeping never
costs me the action itself.

#### Acceptance criteria

- **AC-OFFICE-TASK-PROVENANCE-003.1:** The system shall preserve the existing
  newest-first ordering of the status-change read, and shall not reorder it. The
  Started and Completed derivation is order-independent, taking the earliest and
  latest matching event, and the response timeline array shall inherit the
  order it has today.
- **AC-OFFICE-TASK-PROVENANCE-003.2:** The system shall introduce no tiebreak
  between events that compare equal, and in particular no random or
  non-column one. Timeline timestamps are formatted to whole seconds, so two
  changes within the same second compare equal; where they tie, the order is
  whatever the existing ordering yields, and the derived timestamps are
  unaffected because both events carry the same second.
- **AC-OFFICE-TASK-PROVENANCE-003.3:** A status update shall write exactly one
  activity row per call, as today. Re-issuing the same status update produces a
  second row per AC-OFFICE-TASK-PROVENANCE-001.7; this capability does not make
  the write idempotent. Task creation's idempotency remains `external_id`'s,
  governed by AC-OFFICE-TASK-PROVENANCE-002.11.
- **AC-OFFICE-TASK-PROVENANCE-003.4:** When two callers update the same task's
  status concurrently, each shall read its own pre-status before its own write,
  so that each row's `old_status` is that caller's observed pre-state. The
  system shall not serialize them and shall not add a compare-and-set: the
  activity log is an append-only record of what each caller observed, and a
  lost-update guard on status is outside this capability. For task creation the
  pair is written by the single INSERT, so no interleaving is possible.
- **AC-OFFICE-TASK-PROVENANCE-003.5:** If any provenance write fails, whether
  the activity insert, the event publish, or the creator projection, then the
  primary operation shall still succeed. Provenance is best-effort and shall not
  be able to fail a status update or a task creation, matching the existing
  best-effort guards on the same writers.
- **AC-OFFICE-TASK-PROVENANCE-003.6:** The default for every field this
  capability adds shall be the empty string, in the database, in the DTO, and on
  the wire. No field shall default to a guessed value.

## Out of scope

Each exclusion is a contract, not an oversight.

- **Reviving a `from`-based Completed-timestamp clearing rule in the sidebar
  derivation.** This was the original motivation for `old_status`, and it is
  deliberately excluded. The reopen symptom is now handled by clearing the
  Completed timestamp whenever the task's *current* status is not in the done
  bucket, which is strictly more robust than a timeline-derived rule: it holds
  even when the reopening event has aged out of the activity window. Adding the
  `from` rule back would introduce a second, weaker authority over the same
  field. A follow-up would need to justify why the timeline should override the
  live status, and no current evidence supports that.
- **The activity read window itself.** The status-change read takes the newest
  50 activity entries of *any* action and filters to `task_status_changed`
  afterwards, so on a task with heavy non-status activity the status history,
  and therefore the sidebar's **Started** value, can fall out of the window and
  disappear. This is a real pre-existing defect on the same surface, independent
  of both parts of this capability, and fixing it means filtering by action in
  SQL or paginating. It is named so that it is a known exclusion rather than a
  silent one.
- **Rendering the response timeline array in the web UI.** It is a published
  response field with no current web consumer.
  AC-OFFICE-TASK-PROVENANCE-001.8 keeps it correct; building a UI for it is
  separate work.
- **A `task_created` activity entry.** Rejected as the storage mechanism in the
  paired system design. Emitting one additionally, as a feed entry, is not part
  of this capability.
- **Authorization or filtering by creator**
  (AC-OFFICE-TASK-PROVENANCE-002.17), and any "created by me" view or query
  parameter.
- **Retroactive backfill of the creator pair**
  (AC-OFFICE-TASK-PROVENANCE-002.6).
- **Changing `tasks.origin`** or reconciling its vocabulary with
  `created_by_type`. The two are complementary, origin being the *kind* of
  creation and the creator pair the *identity*, and merging them is a larger
  contract change owned by the task system.
