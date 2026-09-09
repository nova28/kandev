---
id: "05-color-menu-writes-task"
title: "Point the colour menu at the task record"
status: pending
wave: 4
depends_on: ["04-sidebar-color-resolution"]
plan: "plan.md"
spec: "../../specs/shared-task-color/spec.md"
acceptance_criteria:
  - AC-13
  - AC-14
  - AC-15
---

# Task 05: Point the colour menu at the task record

## Summary

Switch the sidebar colour menu from writing the viewer's user settings to
patching `task.color`, and drain the writer's own personal entry for that task so
the colour just chosen is the colour displayed. Update the menu copy to describe
a task-owned value.

## In scope

- `useSetTaskColor` issuing `PATCH /api/v1/tasks/:id` with `color`, then
  removing the writer's own `sidebar_task_colors` entry when one is present.
- Reverting the optimistic marker and showing the existing save-error toast when
  the task `PATCH` fails.
- Menu copy for AC-14 and AC-15, in all five locale catalogs.

## Out of scope

- Removing `users.settings.sidebar_task_colors` or migrating it server-side.
- The resolution order itself (Task 04).

## Acceptance

- Choosing a colour, or None, writes `task.color` and clears the writer's
  personal entry for that task, so the chosen value is what renders.
- A failed task `PATCH` keeps no optimistic value and surfaces the save-error
  toast.
- The menu describes the value as belonging to the task, and the
  automatic-rule notice names the task colour as the value being overridden.

## Verification

```bash
(cd apps && pnpm install --frozen-lockfile)
(cd apps/web && pnpm run typecheck)
(cd apps/web && pnpm exec vitest run hooks/use-task-color.test.tsx components/task/task-switcher-color-menu.test.tsx)
(cd apps/web && pnpm run i18n:check)
(cd apps/web && pnpm run i18n:ratchet)
(cd apps/web && pnpm run lint)
```

## Files likely touched

- `apps/web/hooks/use-task-color.ts`
- `apps/web/components/task/task-switcher-color-menu.tsx`
- `apps/web/src/locales/*/task.json`

## Dependencies

Task 04 — repointing the menu before the sidebar renders `task.color` would make
every colour appear to vanish on write.

## Risks

- If the task `PATCH` succeeds but the personal clear fails, the stale personal
  entry still wins for that viewer and the menu looks broken; show the
  save-error toast and retry the clear on next settings load.
- New copy must land in `pt-pt`, `zh-cn`, `zh-hk`, and `zh-tw` or the build
  fails; use `pnpm run i18n:zh-hant` for the Traditional pair.
- No em dash (U+2014) in user-facing copy; `i18n:check` enforces it.
- The personal store keeps its revision CAS while the task field has none;
  mixing the two write protocols in one handler is the likely defect.

## Parallelism

`sequential`

## Inputs

- Spec sections *Colour resolution* (tier 2 drain), *Failure modes*, and
  AC-13 through AC-15.
- `useSetTaskColor` at `apps/web/hooks/use-task-color.ts`.
- `TaskColorMenu` at `apps/web/components/task/task-switcher-color-menu.tsx`.

## Results

Pending.
