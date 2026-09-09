---
id: "07-e2e-cross-machine"
title: "Cross-machine colour end-to-end evidence"
status: pending
wave: 5
depends_on: ["05-color-menu-writes-task"]
plan: "plan.md"
spec: "../../specs/shared-task-color/spec.md"
acceptance_criteria:
  - AC-9
  - AC-10
---

# Task 07: Cross-machine colour end-to-end evidence

## Summary

Prove the two criteria the card exists for and that unit tests cannot show: a
server-side write with no browser in the write path changes what the sidebar
renders, and a colour set in one browser context reaches an already-open sidebar
in another without a reload.

## In scope

- A desktop spec covering an HTTP `PATCH` carrying only `color`, asserted
  against the rendered marker for a viewer with no matching rule and no personal
  entry.
- Cross-context delivery through the existing `task.updated` broadcast, with no
  reload.
- The same flow at Pixel 5 viewport in a `mobile-*` spec.

## Out of scope

- Re-testing rule evaluation, which `sidebar-automatic-colors.spec.ts` already
  covers and which must keep passing unchanged.
- Any new E2E project or runner configuration.

## Acceptance

- The desktop spec sets a colour without driving the menu and asserts the
  marker changes.
- The second context observes the change without a reload.
- The mobile spec covers the same flow and passes in `mobile-chrome`.

## Verification

```bash
(cd apps && pnpm install --frozen-lockfile)
(cd apps/web && pnpm e2e:run tests/task/sidebar-task-color-sync.spec.ts)
(cd apps/web && pnpm e2e:run --project mobile-chrome tests/task/mobile-sidebar-task-color-sync.spec.ts)
(cd apps/web && pnpm e2e:run tests/task/sidebar-automatic-colors.spec.ts)
```

## Files likely touched

- `apps/web/e2e/tests/task/sidebar-task-color-sync.spec.ts`
- `apps/web/e2e/tests/task/mobile-sidebar-task-color-sync.spec.ts`

## Dependencies

Task 05 — the menu must already write the task record, or the cross-context
assertions measure the personal store.

## Risks

- Driving the colour menu to set the colour would not prove AC-9; the write must
  be an HTTP request with no browser involved.
- A viewer with a matching automatic rule or a personal entry for the task masks
  the task colour, so the fixture must start from neither.
- `mobile-*` specs run only in the `mobile-chrome` project and must be a
  separate file; local runners enforce one worker per shard, so do not pass
  all-worker overrides.
- `apps/node_modules` is absent in this worktree; install once before the first
  package command.

## Parallelism

`parallel-safe` with Task 06 — `apps/web/e2e` versus `docs/`, no shared files.

## Inputs

- Spec section *Verification* and AC-9, AC-10.
- Existing `apps/web/e2e/tests/task/sidebar-task-color-sync.spec.ts` and its
  mobile sibling.
- `apps/web/e2e/README.md` for project and shard rules.

## Results

Pending.
