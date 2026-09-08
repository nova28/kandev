---
status: specified
created: 2026-09-08
owner: kandev
---

# Office Task Provenance: status-change `old_status` and task creator identity

Two provenance gaps on the Office task detail surface, both requiring a contract
or schema change. They are specified together because they share one read
surface (the task detail sidebar / activity feed) and one design question —
*persist the fact, or derive it from the activity timeline* — which this spec
answers once and applies to both.

- **Part A** — the office direct status-update path records only *what a task
  changed to*, never *what it changed from*.
- **Part B** — the sidebar's **Created by** row has no backing data at any
  layer and is permanently blank.

## Prior art

**Wiki leg — receipt.** Searched for the `wiki-query` skill under
`~/.claude/skills`, `~/.claude/skills/gstack`, `~/.config` and `~/.gstack`: not
installed on this runner. Searched for the vault: `~/.obsidian-wiki/` does not
exist (so the `~/.obsidian-wiki/config` symlink the skill resolves through is
absent), `OBSIDIAN_VAULT_PATH` and every `obsidian|vault|qmd` environment
variable are unset, and the only vault path present anywhere on this machine is
a `/Users/henry/Projects/Personal/llmwiki/obsidian-wiki` project entry in
`~/.codex/config.toml` — a path on the primary box, not on this SSH runner.
**No `OBSIDIAN_VAULT_PATH` resolved and no QMD collection was queried**: the
vault is unreachable from here, not empty. This leg did not run, and its absence
is a gap in this spec rather than evidence that no prior reasoning exists.

**saas-kb leg — receipt.** Intended queries were "task provenance / created by
attribution" and "status change audit trail" with `category: "ai_sdlc"`. Neither
was issued: the `saas-kb` MCP server is not present in this session's tool list
and is not configured in `~/.claude.json` (`mcpServers` is empty) or
`~/.codex/config.toml`. **No queries were run.**

**In-repo prior art (this did run).** Three existing contracts settle questions
this spec would otherwise re-derive, and it follows all three:

- `docs/specs/office/requirements/participant-seat-provenance.md` (2026-09-02)
  is the closest neighbour: it records *who created a row* to disambiguate two
  independent writers. Part B adopts its framing — provenance is a property of
  the row, written by whoever creates it, not inferred by a reader.
- `docs/specs/task-cost-ledger/spec.md` AC-4 establishes the house rule that an
  *unobserved* value must stay distinguishable from a *measured* one rather than
  collapsing to a zero value. AC-A4 and AC-B7 below apply exactly that rule.
- `internal/office/dashboard/decisions.go` `resolveDeciderName` is the existing
  in-repo pattern for turning a `(type, id)` actor pair into a display label
  with a raw-id fallback. AC-B9 reuses it rather than inventing a second one.

**What we are doing differently.** The card proposed reviving a `From`-based
`completedAt` clearing rule inside `deriveTaskTimestamps` once `old_status`
lands. We are not doing that; see *Out of scope*. The reason is new evidence:
the reopen symptom has since been fixed by a current-status guard that does not
depend on the timeline at all, and is strictly more robust than the rule the
card proposed.

## Why

### Part A

`ListStatusChanges` (`internal/office/dashboard/service.go:788-816`) builds
every timeline event's `From` from the activity row's `details["old_status"]`.
Exactly one producer writes that key:

- `handleTaskMoved` (`internal/office/service/event_subscribers.go`) writes
  `{"new_status":…,"old_status":…}` — reached only by Kanban-style workflow
  moves.
- `logTaskStatusChangeActivity`
  (`internal/office/dashboard/service_tasks.go:663-690`) writes
  `fmt.Sprintf("{\"new_status\":%q}", req.NewStatus)` — **no `old_status`**.
  This is the path taken by the Office status picker, `PATCH
  /api/v1/office/tasks/:id`, and every agent/MCP status write via
  `UpdateTaskStatusAsAgent`.

So on the path Office users actually take, every status-change event is
half-recorded. `UpdateTaskStatus` already reads the prior state into `preStatus`
(`service_tasks.go:617-620`) for `runReactivityForStatus` and
`maybeSupersedeOnRework`, and simply does not forward it to the activity writer
or to `publishTaskStatusChanged` (`service_tasks.go:963-985`).

The user-visible consequence today is in the workspace activity feed
(`apps/web/app/office/workspace/activity/activity-row.tsx:106-119`), which can
only render "changed status to Done" where it has the data to render "In
Progress → Done". The `timeline` array on `GET /office/tasks/:id` is likewise
half-populated; it currently has no web consumer, but it is a published response
field.

**The card's original symptom is already fixed and is not re-opened here.**
`handler.go:488-491` now clears `CompletedAt` whenever the task's *current*
status is not in the done bucket, so marking a task Done and setting it back to
In Progress no longer leaves a stale Completed timestamp. `completed_at_reopen_test.go`
covers it. Part A is therefore about the missing provenance itself, not about
that timestamp.

### Part B

There is no creator identity on a task row at any layer:

- The `tasks` table has no creator column (`internal/task/repository/sqlite/base_schema.go:416-434`
  plus every `ALTER TABLE tasks ADD COLUMN` in the two migration lists).
- `TaskSearchResult` / `TaskRow` project no creator field
  (`internal/office/repository/sqlite/tasks.go:230-256`).
- `task_created` exists only as a routine/automation *run status*
  (`internal/office/models/enums.go:51`, `internal/automation/models.go:47`),
  never as an activity-log action. No producer writes one.
- `apps/web/app/office/tasks/[id]/map-office-task.ts:65` hard-codes
  `createdBy: ""`, and `apps/web/components/task/simple/task-properties.tsx:142-144`
  renders it as `--` behind `testId="created-by-row"`.

The identity to record *does* now exist upstream, which is what makes this
buildable: `authn.Identity` (`internal/auth/authn/identity.go`) rides every
request context, with a synthetic `default-user` admin injected while auth is
disabled, and the office agent action path already carries `runCtx.AgentID`
(`internal/office/runtime/actions.go:81-118`). `tasks.origin`
(`manual | agent_created | routine | onboarding | automation_run | automation_task`)
records the *kind* of creator but never the *identity*.

## What

### Design decision: a persisted column, not a derived timeline event

Part B could be met by writing a `task_created` activity entry and deriving the
sidebar value the way Started/Completed are derived. **It must not be.** Two
pieces of measured evidence:

1. `ListActivityEntriesByTarget` (`internal/office/repository/sqlite/activity.go:124-141`)
   reads `ORDER BY created_at DESC LIMIT 50` and is **not filtered by action**.
   The creation entry is the oldest row a task will ever have, so on any task
   that accumulates 50 activity rows it is evicted from the window and a derived
   `createdBy` silently reverts to blank — failing hardest on exactly the
   long-lived tasks where provenance matters most.
2. The value is wanted in list projections, and deriving it would put a
   per-row activity query behind every task list.

The same reasoning is why Part A *keeps* recording `old_status` in the activity
row (that is where the reader already looks) but does **not** move the
reopen-detection logic back onto the timeline.

### Part A — Status-change provenance

Vocabulary note, because the two values are computed in different alphabets and
a builder would otherwise have to guess: `req.NewStatus` is the office canonical
lowercase status (`done`, `in_progress`, …), while `preStatus` is the persisted
`tasks.state` enum (`COMPLETED`, `IN_PROGRESS`, …). `dbStateToOfficeStatus`
(`service_tasks.go:821-840`) converts the latter to the former.

- **AC-A1** (Event-driven) WHEN the office direct status-update path records a
  `task_status_changed` activity entry, the system SHALL include an `old_status`
  key in the entry's `details` JSON alongside the existing `new_status` key.
- **AC-A2** (Ubiquitous) `old_status` SHALL be expressed in the same vocabulary
  as `new_status` on the same entry — the office canonical lowercase status —
  by converting the pre-update `tasks.state` through `dbStateToOfficeStatus`.
  A producer SHALL NOT emit one key in the persisted-state alphabet and the
  other in the canonical alphabet.
- **AC-A3** (Ubiquitous) `old_status` SHALL be the task's state as read
  *before* the update was persisted, and SHALL NOT be affected by the approval
  gate's in-place redirect of the requested status. WHEN a `done` request is
  redirected to `in_review` by `applyApprovalGate`, the entry SHALL record
  `new_status: "in_review"` and the genuine pre-update status as `old_status`.
- **AC-A4** (Unwanted) IF the pre-update state cannot be determined — the task
  row is missing, or the lookup errors, leaving `preStatus` empty — THEN the
  system SHALL **omit the `old_status` key entirely** rather than emit an empty
  or defaulted value. `dbStateToOfficeStatus("")` returns `"backlog"`, so
  converting an unknown pre-state would fabricate a transition out of Backlog
  that never happened. An absent key is how "not observed" is expressed, and
  `details["old_status"]` already yields `""` for absent keys, so existing
  readers are unaffected.
- **AC-A5** (Event-driven) WHEN the office direct status-update path publishes
  `OfficeTaskStatusChanged`, the event payload SHALL carry an `old_status` field
  holding the same value, under the same rules, as the activity entry for that
  same update. WHEN the value is unknown per AC-A4, the field SHALL be the empty
  string (the payload is a fixed-key map; absence is not expressible there).
  This field is not write-only: the event has exactly one subscriber,
  `gateway/websocket/office_notifications.go:53`, which re-broadcasts it verbatim
  to browsers as the `office.task.status` WS frame (`subscribe`, not
  `subscribeWithout`, so every payload key is forwarded). `old_status` therefore
  reaches the client on the live-update path as well as the refetch path.
- **AC-A6** (Ubiquitous) The workflow-move producer's existing `old_status`
  behaviour SHALL be left unchanged. This spec adds a second producer of the
  key; it does not redefine the first.
- **AC-A7** (Ubiquitous) The office direct status-update path SHALL continue to
  record an activity entry even when the requested status equals the current
  status, as it does today. This is stated to close the question rather than
  leave it silent: `handleTaskMoved` suppresses same-name moves and the
  direct path does not, and a builder holding both in view would otherwise have
  to invent which one governs. Such an entry SHALL carry `old_status` equal to
  `new_status`.
- **AC-A8** (Event-driven) WHEN a task detail response is built, every timeline
  event derived from an activity entry that carries `old_status` SHALL expose it
  as the event's `from` field, for entries written by either producer.
- **AC-A9** (Event-driven) WHEN the workspace activity feed renders a
  `task_status_changed` entry whose details carry a non-empty `old_status`, it
  SHALL render the transition using both statuses, each resolved through the
  existing shared status-label map. IF `old_status` is absent or empty, THEN it
  SHALL render today's single-status phrasing unchanged. Both forms SHALL be a
  single localization key each, so translation is not order-frozen to English.
- **AC-A10** (Ubiquitous) No status-change behaviour other than the recorded
  details SHALL change: the persisted state, the approval gate, the reactivity
  pipeline, rework supersession, and the sidebar's Started/Completed derivation
  SHALL all behave exactly as they do today.

### Part B — Task creator provenance

#### Storage

- **AC-B1** (Ubiquitous) The `tasks` table SHALL carry two creator columns,
  `created_by_type TEXT NOT NULL DEFAULT ''` and
  `created_by_id TEXT NOT NULL DEFAULT ''`, added through the same idempotent
  `ALTER TABLE … ADD COLUMN` migration mechanism that already carries
  `assignee_user_id`.
- **AC-B2** (Ubiquitous) The columns SHALL survive the office priority-rebuild
  migration (`internal/office/repository/sqlite/base_migrations.go`, the
  `tasks_priority_new` `CREATE`/`INSERT … SELECT`/`DROP`/`RENAME` sequence),
  which recreates the whole table and silently drops any column absent from its
  new shape. This SHALL be satisfied either by registering the two `ADD COLUMN`
  statements so that they run *after* the rebuild, or by carrying both columns
  through the rebuild's column list and `INSERT … SELECT`. A migration test
  SHALL assert the columns are present and their values preserved on a database
  that goes through the rebuild, following the existing
  `migrations_priority_external_id_test.go` pattern.
- **AC-B3** (Ubiquitous) `created_by_type` SHALL be one of
  `user`, `agent`, `routine`, `automation`, `plugin`, `system`, or the empty
  string. The empty string means *creator not recorded* and is the value for
  every row created before this feature.
- **AC-B4** (Ubiquitous) `created_by_id` SHALL identify the creator within its
  type: a `users.id` for `user`, an `agent_profiles.id` for `agent`, a routine id
  for `routine`, an automation id for `automation`, a plugin id for `plugin`.
  For `system` it SHALL be the empty string. There is exactly one agent id space
  and every producer in AC-B8 writes into it: office's "agent instance" is a row
  in `agent_profiles` (`GetAgentInstance` selects `FROM agent_profiles`,
  `internal/office/repository/sqlite/agents.go:185-193`), which is the same id
  space as the `agent_profile_id` that `RunnerProjection`
  (`internal/office/repository/sqlite/base.go:34-50`) surfaces as a task's
  `assignee_agent_profile_id`. A builder SHALL NOT introduce a second agent id
  space or a translation between them.
- **AC-B5** (Ubiquitous) The creator pair SHALL be written once, by the INSERT
  that creates the task, and SHALL be immutable thereafter. No update, move,
  reassign, archive, or reopen path SHALL modify either column.
- **AC-B6** (Ubiquitous) The system SHALL NOT backfill a creator for rows that
  predate this feature. Retroactive attribution SHALL NOT be inferred from
  `tasks.origin`, from activity rows, or from the workspace owner.

#### Determining the creator

- **AC-B7** (Ubiquitous) The creator pair SHALL be supplied **explicitly** by
  the entry point that creates the task, as two new fields on the task-creation
  request, and SHALL NOT be inferred inside the task service from the request
  context identity. The reason is measured: `internal/mcp/scope/scope.go:107-134`
  mints the *workspace owner's* `authn.Identity` for an in-session agent MCP
  stream, so a context-derived creator would attribute agent-created tasks to a
  human who did not create them.
- **AC-B8** (Ubiquitous) Entry points SHALL map to a creator pair as follows.
  A path not named here, or one that supplies nothing, falls under AC-B10.

  | Entry point | `created_by_type` | `created_by_id` |
  | --- | --- | --- |
  | Office agent action — `runtime.Actions.CreateTask` (root and subtask) | `agent` | `runCtx.AgentID` |
  | Agent child-task delegation — `Service.CreateChildTask` (`origin: agent_created`) | `agent` | `parent.AssigneeAgentProfileID` — the parent task's runner, i.e. the agent that delegated |
  | `create_task_kandev` over a task-bound MCP stream | `agent` | the agent profile id of the calling session's task, resolved from the MCP server's bound `taskID`/`sessionID` |
  | `create_task_kandev` over a credentialed non-task stream | `user` | `authn.Identity.UserID` |
  | Human creation from the web UI | `user` | `authn.Identity.UserID` |
  | Routine-generated task | `routine` | the routine id |
  | Automation-generated task (`origin: automation_run`, `automation_task`) | `automation` | the automation id |
  | Plugin / webhook host write (`internal/plugins/host_write.go`) | `plugin` | the plugin id |
  | Onboarding and other kandev-internal creation (`origin: onboarding`) | `system` | `""` |

  Two of those rows need their fallback stated, because the identity is not
  unconditionally present. `CreateChildTask` receives only `parent *models.Task`
  and a `ChildTaskSpec` — it has no session or run context — so
  `parent.AssigneeAgentProfileID` is the only creator identity in scope, and when
  the parent has no runner it is empty. Likewise the MCP row's agent profile may
  not resolve on a stream with no bound task. IF either id resolves empty, THEN
  the pair SHALL be stored as `("", "")` per AC-B10 — never as a bare `agent`
  type with no id, and never substituted with the *new* task's assignee, which is
  the agent that will do the work rather than the one that created it.
- **AC-B9** (Ubiquitous) WHILE authentication is disabled, the synthetic
  identity's `default-user` SHALL be recorded as a normal `user` creator. It is
  the actual single user of that installation, and recording it keeps the value
  correct after authentication is later enabled without a data migration.
- **AC-B10** (Unwanted) IF an entry point supplies a `created_by_type` with an
  empty `created_by_id` for any type other than `system`, or supplies an id with
  no type, THEN the system SHALL store the pair as `("", "")` rather than
  persist a half-identity. A partially-known creator is recorded as unknown.
- **AC-B11** (Unwanted) IF a creation request is deduplicated by `external_id`
  and returns the pre-existing task, THEN the creator pair on that task SHALL
  remain the **first** creator's and SHALL NOT be overwritten by the second
  caller's. Creator provenance follows AC-B5's immutability, not last-writer-wins.

#### Read surface

- **AC-B12** (Ubiquitous) The office task row projection SHALL carry both
  creator columns, and the office task DTO SHALL expose three fields: the raw
  `createdByType` and `createdById`, and a resolved human-readable
  `createdBy` label.
- **AC-B13** (Event-driven) WHEN the `createdBy` label is resolved, the system
  SHALL use: for `user`, the user's display name, falling back to their email;
  for `agent`, the agent instance's name; for `routine`, `automation` and
  `plugin`, that entity's name; for `system`, a localized "System" label.
- **AC-B14** (Unwanted) IF the referenced user, agent, routine, automation or
  plugin cannot be resolved — deleted, or no longer readable — THEN the system
  SHALL fall back to the raw `created_by_id` rather than return an empty label,
  matching the documented fallback of `resolveDeciderName`. A creator that was
  recorded SHALL never render as unknown merely because its referent is gone.
- **AC-B15** (State-driven) WHILE `created_by_type` is the empty string, the
  DTO's `createdBy` SHALL be the empty string and the sidebar's **Created by**
  row SHALL render its existing `--` placeholder. The row SHALL NOT be hidden:
  a task with no recorded creator is different from a task with no such concept,
  and the row is the only place that difference is legible.
- **AC-B16** (Ubiquitous) `apps/web/app/office/tasks/[id]/map-office-task.ts`
  SHALL map `createdBy` from the DTO instead of hard-coding `""`, and its unit
  test asserting the hard-coded empty string SHALL be replaced by tests covering
  a populated creator and an unrecorded one.
- **AC-B17** (Ubiquitous) Recording and reading a creator SHALL NOT change task
  visibility, authorization, or scoping. `created_by_*` is descriptive only; no
  read path SHALL filter or authorize on it in this feature.

### Cross-cutting behaviour

Stated explicitly so no builder has to invent them.

- **AC-X1** (Ubiquitous) **Ordering.** `ListStatusChanges` returns events in the
  order `ListActivityEntriesByTarget` yields them: `ORDER BY created_at DESC`,
  i.e. **newest first**, and this spec SHALL NOT change it. `deriveTaskTimestamps`
  is order-independent (it takes the minimum `At` for started and the maximum for
  completed), and the response `timeline` array inherits the same newest-first
  order it has today.
- **AC-X2** (Ubiquitous) **Tiebreak.** Timeline `At` values are formatted to
  whole seconds (`"2006-01-02T15:04:05Z"`), so two status changes within the
  same second compare equal as strings. This spec introduces no tiebreak and
  SHALL NOT introduce a random or non-column one: where two events tie, the
  order is whatever `created_at DESC` yields, and the derived timestamps are
  unaffected because both events carry the same second.
- **AC-X3** (Ubiquitous) **Idempotency.** Neither change is retried in a way that
  can double-write. A status update writes exactly one activity row per call, as
  today — re-issuing the same status update produces a second row (AC-A7), which
  is existing behaviour and not made idempotent here. Task creation's idempotency
  is `external_id`'s, governed by AC-B11.
- **AC-X4** (Ubiquitous) **Concurrency.** Two callers updating the same task's
  status concurrently each read their own `preStatus` before their own write, so
  each row's `old_status` is that caller's observed pre-state. The system SHALL
  NOT serialize them or add a compare-and-set: the activity log is an append-only
  record of what each caller observed, and a lost-update guard on status is
  outside this feature. For task creation the pair is written by the single
  INSERT, so no interleaving is possible.
- **AC-X5** (Unwanted) IF any provenance write fails — the activity insert, the
  event publish, or the creator projection — THEN the primary operation SHALL
  still succeed. Provenance is best-effort and SHALL NOT be able to fail a
  status update or a task creation, matching `logTaskStatusChangeActivity`'s
  existing `s.activity == nil` guard and `logBlockerActivity`'s precedent.
- **AC-X6** (Ubiquitous) **Defaults.** The default for every new field is the
  empty string, in the database, in the DTO, and on the wire. No field defaults
  to a guessed value.

## Out of scope

Each exclusion is a contract, not an oversight.

- **Reviving a `From`-based `completedAt` clearing rule in
  `deriveTaskTimestamps`.** The card proposed this as the payoff for `old_status`
  (citing the reverted commit `0d3756ed1`). It is deliberately excluded. The
  reopen symptom is now handled at `handler.go:489-491` by clearing `CompletedAt`
  whenever the task's *current* status is not in the done bucket, which is
  strictly more robust than a timeline-derived rule: it holds even when the
  reopening event has aged out of the 50-row activity window that AC-X1
  describes, where a `From`-based rule would silently stop clearing. Adding the
  `From` rule back would introduce a second, weaker authority over the same
  field. A follow-up would need to justify why the timeline should override the
  live status, and no current evidence supports that.
- **The 50-row activity window itself.** `ListStatusChanges` reads the newest 50
  activity entries of *any* action and filters to `task_status_changed`
  afterwards, so on a task with heavy non-status activity the status history —
  including the first `in_progress`, and therefore the sidebar's **Started**
  value — can fall out of the window and disappear. This is a real pre-existing
  defect on the same surface, it is independent of both parts of this spec, and
  fixing it means either filtering by action in SQL or paginating. It is named
  here so it is a known exclusion rather than a silent one; a follow-up should
  start at `internal/office/repository/sqlite/activity.go:124-141`.
- **Rendering the `timeline` array in the web UI.** `TaskResponse.timeline` is a
  published response field with no current web consumer. AC-A8 keeps it correct;
  building a UI for it is separate work.
- **A `task_created` activity entry.** Rejected as the storage mechanism above.
  Emitting one *additionally*, as a feed entry, is not part of this feature.
- **Authorization or filtering by creator** (AC-B17), and any "created by me"
  view or query parameter.
- **Retroactive backfill of `created_by_*`** (AC-B6).
- **Changing `tasks.origin`** or reconciling its vocabulary with
  `created_by_type`. The two are complementary — origin is the *kind* of
  creation, the creator pair is the *identity* — and merging them is a larger
  contract change.

## Observable surfaces (E2E decision input)

User-visible surfaces this touches:

1. **Office workspace activity feed** — `/office/workspace/.../activity`,
   `activity-row.tsx`. A status change renders as a transition (AC-A9). This is
   the only user-visible surface for Part A.
2. **Office task detail sidebar, Created by row** —
   `task-properties.tsx`, `testId="created-by-row"`. Shows a resolved creator
   instead of `--` (AC-B12…B16).
3. **`GET /api/v1/office/tasks/:id`** — `timeline[].from` populated for
   direct-update events (AC-A8); `createdBy` / `createdByType` / `createdById`
   added to the task object.

Both (1) and (2) are rendered surfaces with existing Playwright coverage
patterns under `apps/web/e2e/tests/office/`, and both are only reachable through
a real create-then-mutate flow, so **E2E coverage is warranted**: one spec
asserting a newly created task shows a non-placeholder Created by value, and one
asserting a status change renders as a from→to transition in the activity feed.
Backend contract behaviour (AC-A1…A7, AC-B1…B11, AC-X*) is unit/integration
territory, not E2E.

## Verified inputs

Every claim above was read from the tree at merge-base `18d38c11a`, not assumed.

| Fact | Location |
| --- | --- |
| `From` sourced from `details["old_status"]` | `internal/office/dashboard/service.go:788-816` |
| Direct path writes only `new_status` | `internal/office/dashboard/service_tasks.go:663-690` |
| `preStatus` computed and unused by the writer | `internal/office/dashboard/service_tasks.go:611-633` |
| Event payload keys | `internal/office/dashboard/service_tasks.go:963-985` |
| Workflow-move producer writes both keys | `internal/office/service/event_subscribers.go`, `handleTaskMoved` |
| Approval-gate redirect mutates `req.NewStatus` in place | `internal/office/dashboard/service_tasks.go:685-705` |
| `dbStateToOfficeStatus("")` returns `"backlog"` | `internal/office/dashboard/service_tasks.go:821-840` |
| Current-status guard clears `CompletedAt` | `internal/office/dashboard/handler.go:486-492` |
| Activity read is `created_at DESC LIMIT 50`, unfiltered by action | `internal/office/repository/sqlite/activity.go:124-141` |
| Timeline `At` truncated to seconds | `internal/office/dashboard/service.go:805` |
| Activity feed renders `new_status` only | `apps/web/app/office/workspace/activity/activity-row.tsx:106-119` |
| No creator column on `tasks` | `internal/task/repository/sqlite/base_schema.go:416-434` |
| `TaskSearchResult` has no creator field | `internal/office/repository/sqlite/tasks.go:230-256` |
| `task_created` is a run status, never an activity action | `internal/office/models/enums.go:51`, `internal/automation/models.go:47` |
| Office table-recreate drops columns absent from its new shape | `internal/office/repository/sqlite/base_migrations.go:620-675` |
| Per-request identity, synthetic `default-user` when auth off | `internal/auth/authn/identity.go`, `internal/auth/httpmw/middleware.go:264-266` |
| MCP scope mints the workspace **owner's** identity for agent streams | `internal/mcp/scope/scope.go:107-134` |
| Office agent path carries `runCtx.AgentID` | `internal/office/runtime/actions.go:81-118` |
| `tasks.origin` vocabulary | `internal/task/models/models.go:841-848` |
| `createdBy` hard-coded empty; rendered as `--` | `apps/web/app/office/tasks/[id]/map-office-task.ts:65`, `apps/web/components/task/simple/task-properties.tsx:142-144` |
