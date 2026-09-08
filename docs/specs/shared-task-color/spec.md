---
status: draft
created: 2026-09-08
owner: kandev
---

# Shared, server-owned task colour

Umbrella: [ui](../ui/) · This spec **amends four named passages** in
[`docs/specs/ui/requirements/sidebar-automatic-task-colors.md`](../ui/requirements/sidebar-automatic-task-colors.md)
and [`docs/specs/ui/system-design/sidebar-automatic-task-colors.md`](../ui/system-design/sidebar-automatic-task-colors.md);
Build lands those amendments in the same change. See *Amendments to the sidebar
task-colours contract*. Everything else in those documents is unchanged.

## Corrections to the task brief

The brief — including its own "CITATION CORRECTIONS 2026-09-02" block, which
re-verified the defect at `origin/main` @ `0064b9fb5` — describes a codebase that
no longer exists. `feat(sidebar): add automatic task color rules` (#3180, commit
`3173c2b56`) landed after that verification. Measured against `18d38c11`, the
current `origin/main`:

1. **"Writes only to browser localStorage" is no longer true.** `apps/web/lib/task-colors.ts:1`
   imports `getLocalStorage, removeLocalStorage` — not `setLocalStorage`. Nothing
   writes `kandev.taskColors` any more. The durable store is
   `users.settings.sidebar_task_colors`, a `map[string]*string` validated in
   `apps/backend/internal/user/models/sidebar_task_colors.go` and written through
   `PATCH /api/v1/user/settings` with a bounded `sidebar_task_color_patch`
   (`apps/backend/internal/user/dto/dto.go:168`).

2. **Two of the brief's four acceptance criteria are already met.** Migration of
   existing localStorage colours exists and is not lossy
   (`apps/web/hooks/use-task-color-migration.ts`: bounded 500-entry batches,
   `if_missing: true`, then `clearLegacyTaskColors()`). Colour therefore already
   survives a different browser and a different machine for the same user.

3. **The brief's "RELATED" note shipped.** `priority` is a first-class automatic
   rule dimension (`apps/backend/internal/user/models/sidebar_task_color_automation.go:14`),
   so a user *can* make the sidebar follow priority today. This does not close the
   card: rules are personal and default to none, so an agent that sets priority
   still changes nothing on a board whose owner has configured no rule.

4. **The two criteria that remain unmet are the two the brief cares about**, and
   they were deliberately deferred rather than rejected.
   `docs/plans/sidebar-automatic-task-colors/plan.md` lists under *Out of scope*:
   "Shared workspace colors or a task-owned color field." There is still no
   `color` anywhere in `apps/backend/pkg/api/v1/task.go`, and `httpUpdateTaskRequest`
   (`apps/backend/internal/task/handlers/task_http_handlers.go:1569`) carries
   `Title, Description, Priority, State, Repositories, Position, Metadata,
   ParentID, AssigneeUserID` — no colour. This spec is that deferred work.

5. **The brief's third consumer citation is right and there are more.** Beyond
   `apps/web/hooks/use-task-color.ts` and `apps/web/components/task/task-item.tsx:379-405`,
   colour now also flows through `apps/web/lib/sidebar/task-color-projection.ts`,
   `apps/web/lib/sidebar/task-color-rules.ts`, `apps/web/lib/task-color-presentation.ts`,
   and the mobile switcher sheet. The `SelectionBar` citation is stale: the marker
   is resolved at `task-item.tsx:405` via `resolveTaskItemColor`.

6. **`metadata` is not a viable home for colour.** `PATCH /api/v1/tasks/:id`
   *replaces* the metadata map wholesale — `protectedTaskMetadataUpdate`
   (`apps/backend/internal/task/service/service_task_metadata.go:24-35`) clones the
   request map and preserves exactly one key, `deferred_launch`. Any unrelated
   client patching metadata would silently drop a colour stored there. Colour must
   be a first-class field.

## Why

Colour is the primary visual signal on the task sidebar, and it is the one signal
an agent cannot touch. An agent that triages a board can rank thirteen tasks by
priority through `PATCH /api/v1/tasks/:id` and leave the board looking exactly as
it did, because the thing the board renders is a personal preference living in the
triaging agent's absence.

The remaining gap is narrow and precise: **there is no task-owned colour.** Colour
cannot be a team convention ("red = blocker"), cannot be set by a script, is absent
from every export and backup of the task record, and is not carried by the task API
that every other task property travels through.

## Prior art

**Leg 1 — our own prior reasoning (wiki).** *Not run: the tool is not installed on
this runner.* `ls /Users/neo/.claude/skills/` lists 58 skills and contains no
`wiki-query` (nor any `wiki-*`); `/Users/neo/.obsidian-wiki` does not exist, so
there is no `OBSIDIAN_VAULT_PATH` to resolve and no QMD collection to name. No
grep fallback was possible either, because there is no vault to grep. This is a
missing receipt, not an empty result: if this card is re-run on a runner with the
skill, this leg should be executed before the design decision below is trusted.

**Leg 2 — what other products shipped (saas-kb).** *Not run: the MCP server is not
connected to this session.* The only MCP server available is `kandev`; there is no
`search_fsm_docs` tool and therefore no `category: "ai_sdlc"` slice to query.

**Leg 3 — in-repo prior art (run, and it is the decisive one).** Searched
`grep -rln -i "sidebar.*color|task color" docs/`. This returned the complete design
record for the feature that shipped nine days ago:

- `docs/plans/sidebar-automatic-task-colors/plan.md` (status: done) — names
  "Shared workspace colors or a task-owned color field" as **out of scope**, and
  "Real-time cross-browser delivery of manual color changes" as out of scope too.
- `docs/plans/sidebar-automatic-task-colors/task-05-persist-personal-manual-colors.md`
  and `task-06-adopt-server-backed-manual-colors.md` — the personal-colour work.
- `docs/specs/ui/requirements/sidebar-automatic-task-colors.md` (REQ-…-001…005) and
  `docs/specs/ui/system-design/sidebar-automatic-task-colors.md`, whose *Purpose and
  boundaries* reads: "The UI system owns automatic task colors as a personal
  presentation contract. The feature reads task facts but **never writes task
  records**."

**What we are doing differently, and why.** That boundary was correct for a personal
presentation feature and is wrong for this card, whose entire complaint is that no
shared record exists. We cross it deliberately and narrowly: this spec adds a task
record field and leaves the personal rule engine untouched and still on top. The
shipped work is not unwound — the one thing that changes is which store the task
colour *menu* writes to. We also get, for free, the item that plan listed as out of
scope: task updates already publish `task.updated` (`PublishTaskUpdated`), so a
shared colour is delivered live to other browsers, which a personal setting was not.

## What

Add `color` to the task record, the task API, and both task update surfaces (HTTP
and WebSocket). Repoint the existing sidebar colour menu at it. Keep the personal
automatic rule engine exactly as it is, and keep every existing personal manual
colour readable so nobody loses one.

### Design decision: shared, not per-user

The brief asks this question explicitly and recommends shared. We adopt shared.

- `KANDEV_FEATURES_AUTH` is off in every shipped profile, so in the default
  deployment "personal" and "shared" denote the same person, and a per-user table
  buys nothing while still being unreachable from a script.
- The motivating use is triage ranking by an agent, which is inherently shared.
- Task colour then behaves like every other task property: one value, on the task,
  in the API, in an export, settable by anything that can `PATCH` the task.

**Alternative considered and rejected: a per-(user, task) table.** It is the
technically "correct" model if colour is personal — but it is the model we already
have (`users.settings.sidebar_task_colors`), and it is precisely what fails the
brief's acceptance. Building a second one would restate the defect in a new table.

**Alternative considered and rejected: replacing the personal store outright.**
Deleting `sidebar_task_colors` would either lose colours or require a server-side
bulk migration that, on a multi-user install, publishes one user's private choices
to everyone. We instead retain the personal store as a readable, self-draining
fallback (AC-12, AC-13), which needs no data migration at all.

### Colour resolution

Exactly three sources, resolved per task per viewer, in this order. The first that
yields a value wins.

1. **Automatic rule** — the first enabled, complete, matching rule in the user's
   stored rule order. Unchanged from the shipped contract.
2. **Personal manual colour** — the viewer's `sidebar_task_colors[taskId]`, when
   the key is *present*. Tri-state, and the three states are already representable
   in the stored map:
   - key absent → no personal opinion, fall through to (3);
   - key present, non-null → personal override, wins over (3);
   - key present, null (tombstone) → explicit personal "no marker", suppresses (3).
3. **Shared task colour** — `task.color`, `""` meaning none.

If none yields a value, the task shows no marker.

Tier 2 is transitional by construction: every write through the colour menu removes
the viewer's key for that task (AC-13), so the map drains as tasks are recoloured
and never grows again. It is retained rather than migrated because migrating it
server-side is the unsafe option (see above).

## Data model

One column, mirroring the house migration style at
`apps/backend/internal/task/repository/sqlite/base_migrations.go:218`
(`r.migrate.Apply("tasks.labels", ...)`):

```sql
ALTER TABLE tasks ADD COLUMN color TEXT NOT NULL DEFAULT ''
```

- Applied under the key `tasks.color`.
- `''` is "no colour" and is the value for every pre-existing row and every new task
  that does not supply one.
- `models.Task` gains a `Color string` field tagged `json:"color"`, without
  `omitempty`, matching the adjacent `Priority` field on the same struct.
- The column stores only a validated palette token or `''`. No other value is ever
  written, so the `TEXT` width needs no separate bound.

### Palette

The seven manual tokens, unchanged: `red`, `orange`, `yellow`, `green`, `blue`,
`purple`, `pink`. Tokens are lowercase and matched exactly.

The validator must be a single shared source of truth. `IsValidSidebarTaskColor`
(`apps/backend/internal/user/models/sidebar_task_colors.go:28`) is the existing one;
Build either calls it or relocates it, but must not fork a second token list. The
ten-token automatic palette (which adds `gray`, `cyan`, `indigo`) is **not** used
here — it belongs to rule outputs and stays there.

## API surface

### Read

`TaskDTO` (`apps/backend/pkg/api/v1/task.go`) gains:

```go
Color string `json:"color"`
```

Deliberately **without** `omitempty`, exactly like the adjacent `Priority` field
(tagged `json:"priority"`, also without `omitempty`), so that "no colour" is transmitted as an explicit `""` and a
client can always distinguish "cleared" from "field absent because the server is
older". Every endpoint that already returns a `TaskDTO` returns `color` with no
further change.

### Write

`color *string` is added to all three request structs, which must stay in lockstep:

| Struct | File |
| --- | --- |
| `httpUpdateTaskRequest` | `apps/backend/internal/task/handlers/task_http_handlers.go:1569` |
| the WS update request | `apps/backend/internal/task/handlers/task_ws_handlers.go:346` |
| `service.UpdateTaskRequest` | `apps/backend/internal/task/service/service_requests.go:127` |
| `dashboard.UpdateTaskRequest` (Office `PATCH /office/tasks/:id`) | `apps/backend/internal/office/dashboard/handler.go:566` |

`CreateTaskRequest` (`apps/backend/pkg/api/v1/task.go:176`) also gains an optional
`color`, for symmetry with `priority`, which is already there.

The Office task surface is included because it already carries `Priority` as a
`*string` with the same omitted/empty convention. Leaving it out would make the
brief's requirement — settable through the same update path as priority — true of
only one of the two paths that carry priority.

Semantics follow the `ParentID` / `AssigneeUserID` precedent on this same endpoint —
a `*string` where the empty string is the clear:

- field omitted (or JSON `null`, which is indistinguishable from omitted for a Go
  `*string`) → colour unchanged;
- `""` → colour cleared to none;
- a valid token → colour set;
- anything else → `400`, no write.

Validation happens before any mutation, mirroring `ValidateTaskPriority` at
`service_tasks.go:1927-1931`, so a rejected colour never partially applies alongside
other fields in the same request.

## Ordering, idempotency, concurrency

**Ordering.** A task has exactly one colour, so this feature introduces no new
ordering and needs no tiebreak. The only ordered structure in the resolution path is
the automatic rule array, whose "first enabled match in stored array order" contract
is unchanged and is not re-specified here.

**Idempotency.** Setting a colour is idempotent in stored state: N identical writes
leave the same value. It is deliberately *not* special-cased into a no-op —
`task.updated_at` advances and `task.updated` publishes on every accepted write,
including one that changes nothing, because that is already the behaviour of every
other field on this endpoint and a colour-only exception would be a surprise. There
is no request-level idempotency key; the operation is naturally idempotent.

**Concurrency.** Two callers writing `color` on the same task: both succeed, last
writer wins, and `updated_at` orders them. There is no revision token and no CAS on
the task update path, and this spec does not add one — it adopts exactly the
concurrency semantics `priority` already has. This is a deliberate asymmetry with
`PATCH /api/v1/user/settings`, which *does* use revision CAS
(`compareUserSettingsRevisions`); the personal store keeps its CAS, the task field
does not get one.

A colour write racing a rule change is not a conflict: the two live in different
stores and are composed at render time, so the loser of neither race is lost.

## Failure modes

| Condition | Behaviour |
| --- | --- |
| Unknown / mis-cased colour token | `400`, message names the field and the seven allowed tokens; no field in the request is applied |
| Task not found | `404` via the existing `handleNotFound` |
| Caller lacks task write scope | Existing `authorizeTaskScope(ctx, id, authz.ScopeTaskWrite)` rejection, unchanged |
| Task `PATCH` fails from the colour menu | No optimistic value is kept; the marker reverts and the existing save-error toast shows |
| Task `PATCH` succeeds, the follow-up personal clear fails | The shared value is stored; the stale personal entry still wins for this viewer, so the marker does not visibly change. The client shows the save-error toast and retries the clear on next settings load |
| Older client, newer server | Ignores an unknown `color` field on read; cannot set colour. No migration needed |

## Permissions

Reading colour requires the task read scope already needed to read the task.
Writing colour requires `ScopeTaskWrite`, already enforced for every other field on
this endpoint — colour adds no new scope and no admin gate. A user who can rename a
task can colour it.

## Acceptance criteria

**AC-1:** The `tasks` table shall have a `color` column, applied under the migration
key `tasks.color`, defaulting to `''` for every pre-existing and newly created row.

**AC-2:** `TaskDTO` shall include `color` as a non-omitempty JSON string on every
endpoint that returns a task, so a task with no colour serialises as `"color": ""`.

**AC-3:** `PATCH /api/v1/tasks/:id` shall accept `color` and persist it, and the same
field shall be accepted by the WebSocket task-update action with identical semantics.

**AC-4:** `POST /api/v1/tasks` shall accept an optional `color`, and shall create the
task with `color` `''` when it is omitted.

**AC-5:** Omitting `color` from an update shall leave the stored colour unchanged;
sending `""` shall clear it to none.

**AC-6:** A `color` that is not one of `red`, `orange`, `yellow`, `green`, `blue`,
`purple`, `pink` — including a correctly-spelled token in the wrong case — shall be
rejected with `400`, and no other field in the same request shall be applied.

**AC-7:** Setting the same colour twice shall leave the same stored value, and each
accepted write shall advance `updated_at` and publish `task.updated`.

**AC-8:** Two concurrent colour writes to one task shall both succeed, with the last
write persisted; no revision token shall be required by, or added to, this path.

**AC-9:** Colour shall be settable with no browser involved — a `curl` `PATCH`
carrying only `color` shall change what the sidebar renders for a viewer who has no
matching automatic rule and no personal entry for that task.

**AC-10:** A colour set on one machine shall be visible on a different machine and a
different browser, and shall arrive in an already-open sidebar through the existing
`task.updated` broadcast without a reload.

**AC-11:** The sidebar marker shall resolve as: first matching enabled automatic
rule; else the viewer's personal manual entry when that key is present (a null
tombstone yielding no marker); else `task.color`; else no marker.

**AC-12:** An existing personal manual colour shall keep rendering for its own
viewer after this change, and shall not be copied into `task.color` by any
server-side migration.

**AC-13:** Choosing a colour — or None — in the task colour menu shall write
`task.color`, and shall additionally remove the writing viewer's own
`sidebar_task_colors` entry for that task when one is present, so that the value
just chosen is the value displayed.

**AC-14:** The colour menu shall present the seven-token palette and shall describe
the value it edits as belonging to the task rather than to the viewer.

**AC-15:** When an automatic rule is supplying the colour, the menu shall continue to
say so and to explain that the chosen colour does not override a matching rule, now
naming the task colour as the value being overridden.

**AC-16:** No automatic rule evaluation shall write `task.color`, and enabling,
disabling, reordering or deleting a rule shall leave every stored `task.color`
untouched.

**AC-17:** The seven-token palette shall have exactly one definition in the backend;
no second token list shall be introduced for the task field.

**AC-18:** `PATCH /office/tasks/:id` shall accept `color` with the same
omitted-unchanged / empty-clears / invalid-rejected semantics as the task endpoint,
so that every request path which today carries `priority` also carries `color`.

**AC-19:** A viewer who lacks task write scope shall still see the resolved colour
and shall not be offered a colour control that fails on use; the write path shall
reject such a write with the existing scope error rather than silently no-op.

## Amendments to the sidebar task-colours contract

Build lands these edits in the same change. They are the only permitted edits to
those two frozen documents.

1. `docs/specs/ui/requirements/sidebar-automatic-task-colors.md`, **Terminology** —
   "Effective color" gains the third source: the first matching automatic colour;
   else the manual colour when the viewer holds one; else the task colour.
2. Same file, **AC-UI-SIDEBAR-AUTOMATIC-TASK-COLORS-002.8** — "When no rule matches,
   the task shall show its manual color or no marker" becomes "… its manual color,
   otherwise its task color, or no marker."
3. Same file, **AC-UI-SIDEBAR-AUTOMATIC-TASK-COLORS-002.10** — the guarantee that
   automatic colours "shall not mutate a task" is *retained verbatim*; a note records
   that it constrains rule evaluation only, and not the colour menu, which now writes
   the task record by design.
4. `docs/specs/ui/system-design/sidebar-automatic-task-colors.md`, **Purpose and
   boundaries** — "The feature reads task facts but never writes task records" is
   scoped to rule evaluation, and the manual-colour write path is recorded as
   targeting `PATCH /api/v1/tasks/:id`.

## Out of scope

- **A `color` argument on the MCP `update_task_kandev` / `create_task_kandev` tools.**
  The brief's bar is "the same update path as priority", and `priority` is not on
  those tools either; adding colour alone would be an odd asymmetry. An agent sets
  colour over HTTP today. A follow-up should add `priority` and `color` together.
- **Removing `users.settings.sidebar_task_colors`.** It is retained and still read
  (AC-12). It drains as tasks are recoloured (AC-13); deleting the field, and the
  release gate that would need, is separate work.
- **Bulk / multi-task colour writes.** One task per request. A board-wide recolour
  is N requests, which is acceptable for a triage pass and avoids a new endpoint.
- **Widening the task field to the ten-token automatic palette.** `gray`, `cyan` and
  `indigo` remain rule-output-only.
- **Per-workspace or per-view colour overrides**, and any colour concept above the
  single task.
- **Colour history, or attribution of who set a colour.** `task.color` is a plain
  value; `updated_at` is the only trace.
- **Making priority drive the sidebar marker by default.** The brief raises it; the
  rule engine already supports it as an opt-in dimension, and turning it on for
  everyone would change every existing board without being asked.

## Verification

E2E decision input — user-visible surfaces this touches:

- the desktop sidebar task context menu's Colour submenu
  (`apps/web/components/task/task-switcher-color-menu.tsx`);
- the sidebar task row marker (`apps/web/components/task/task-item.tsx:405`);
- the mobile session task-switcher sheet
  (`apps/web/components/task/mobile/session-task-switcher-sheet-item.ts`).

Existing E2E that must keep passing, and is the natural place to extend:
`apps/web/e2e/tests/task/sidebar-automatic-colors.spec.ts` and
`apps/web/e2e/tests/task/mobile-sidebar-automatic-colors.spec.ts`.

E2E is warranted: AC-9 and AC-10 are cross-surface and cross-machine by definition
and cannot be shown by unit tests alone. AC-9 in particular is the criterion the
whole card exists for — a server-side write with no browser involved changing what
the browser renders.
