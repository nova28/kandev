---
id: "04-sidebar-color-resolution"
title: "Resolve the shared task colour in the sidebar"
status: pending
wave: 3
depends_on: ["02-task-color-write-paths"]
plan: "plan.md"
spec: "../../specs/shared-task-color/spec.md"
acceptance_criteria:
  - AC-11
  - AC-12
  - AC-16
---

# Task 04: Resolve the shared task colour in the sidebar

## Summary

Carry `color` on the frontend task types and extend the sidebar marker
resolution to a third tier, so a task colour set on the server renders for a
viewer who has no matching automatic rule and no personal entry. The rule engine
and every existing personal colour keep working untouched.

## In scope

- `color` on the HTTP task type and the kanban task type, beside `priority`.
- `useTaskColor` resolving: personal entry when the key is *present* (a null
  tombstone yielding no marker), otherwise `task.color`, otherwise none.
- Keeping the automatic tier above both, unchanged, in `resolveTaskItemColor`.

## Out of scope

- The colour menu's write path and copy (Task 05).
- Any change to rule evaluation, rule storage, or the automatic palette.

## Acceptance

- All four resolution branches behave as specified, including the null
  tombstone suppressing the task colour.
- An existing personal manual colour still renders for its own viewer and is
  not copied into `task.color`.
- Enabling, disabling, reordering, or deleting a rule writes no task record.

## Verification

```bash
(cd apps && pnpm install --frozen-lockfile)
(cd apps/web && pnpm run typecheck)
(cd apps/web && pnpm exec vitest run hooks/use-task-color.test.tsx lib/sidebar/task-color-rules.test.ts lib/task-colors.test.ts)
(cd apps/web && pnpm run lint)
```

## Files likely touched

- `apps/web/lib/types/http.ts`
- `apps/web/lib/state/slices/kanban/types.ts`
- `apps/web/hooks/use-task-color.ts`
- `apps/web/hooks/use-task-color.test.tsx`

## Dependencies

Task 02 — the API must return and accept a colour before the sidebar can render
one meaningfully.

## Risks

- `apps/node_modules` is absent in this worktree; the install step is required
  before the first package command or every import fails to resolve.
- Collapsing the personal map's tri-state to a truthy check loses the null
  tombstone and silently changes AC-11's fourth branch.
- Touching `task-color-projection.ts` or `task-color-rules.ts` risks AC-16;
  the third tier belongs below rule evaluation, not inside it.

## Parallelism

`parallel-safe` with Task 03 — disjoint files, no shared schema, migration,
generated contract, or lockfile.

## Inputs

- Spec section *Colour resolution* and AC-11, AC-12, AC-16.
- `useTaskColor` at `apps/web/hooks/use-task-color.ts`.
- `resolveTaskItemColor` at `apps/web/lib/task-color-presentation.ts`, called
  from `task-item.tsx:405`.

## Results

Pending.
