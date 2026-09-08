---
id: "02-render-status-transition"
title: "Render the status transition in the workspace activity feed"
status: pending
wave: 2
depends_on: ["01-record-old-status"]
plan: "plan.md"
requirements:
  - REQ-OFFICE-TASK-PROVENANCE-001
acceptance_criteria:
  - AC-OFFICE-TASK-PROVENANCE-001.9
system_design:
  - ../../specs/office/system-design/task-provenance-01.md
---

# Task 02: Render the Status Transition in the Workspace Activity Feed

## Summary

Render a `task_status_changed` entry as a from-to transition when its details
carry a non-empty `old_status`, falling back to today's single-status phrasing
otherwise. Both statuses resolve through the existing shared status-label map.

## In scope

- Add the component test and the E2E assertion before the production change.
- Add one localization key for the whole transition clause, in `en`, `pt-pt`,
  `zh-cn`, `zh-hk`, and `zh-tw`.
- Select the transition form on a non-empty `old_status`, keeping the existing
  `office:activityStatusChangedTo` and `office:statusChanged` paths for entries
  without one.

## Out of scope

- The backend write, which is Task 01.
- Rendering the `timeline` response array, which has no web consumer.
- Any change to `taskStatusLabel` or the shared status-label map.

## Acceptance

- An entry carrying `old_status` renders both statuses through the shared label
  map; an entry without one renders exactly as it does today.
- The transition is a single localization key with `from` and `to`
  interpolations, not two concatenated fragments.
- `pnpm run i18n:check` passes with the key present in all five locales.

## Verification

`apps/node_modules` is absent in this worktree, so install once before the first
package command.

```bash
cd apps
pnpm install --frozen-lockfile

pnpm --filter @kandev/web test -- activity-row
pnpm --filter @kandev/web lint

cd web
pnpm run i18n:check

pnpm e2e:run tests/office/activity-page.spec.ts -- --project=chromium
```

## Files likely touched

- `apps/web/app/office/workspace/activity/activity-row.tsx`
- `apps/web/app/office/workspace/activity/activity-row.test.tsx`
- `apps/web/src/locales/{en,pt-pt,zh-cn,zh-hk,zh-tw}/office.json`
- `apps/web/e2e/tests/office/activity-page.spec.ts`

## Dependencies

Task 01 — the feed cannot render a transition the backend does not send.

## Risks

- Translations gate the build: a key present only in `en` fails
  `check-i18n-keys.mjs`. Use `pnpm run i18n:zh-hant` for the Traditional pair
  rather than hand-translating.
- The existing key's comment (`activity-row.tsx:107-109`) records why this is
  one key and not two fragments. Splitting the clause to reuse the existing key
  would freeze the English word order for every locale.
- Do not use a Unicode em dash in the new copy; `i18n:check` rejects it.

## Parallelism

`sequential`

## Inputs

- AC-OFFICE-TASK-PROVENANCE-001.9.
- `activity-row.tsx:106-120` and the two existing keys in
  `src/locales/en/office.json`.

## Results

Pending.
