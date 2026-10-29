---
id: "18-environment-listing"
title: "Host environment listing (ADMISSION-002)"
status: pending
wave: 6
depends_on: []
plan: "plan.md"
requirements:
  - REQ-EXECUTORS-PLUGIN-ADMISSION-002
acceptance_criteria:
  - AC-EXECUTORS-PLUGIN-ADMISSION-002.1
  - AC-EXECUTORS-PLUGIN-ADMISSION-002.2
  - AC-EXECUTORS-PLUGIN-ADMISSION-002.3
  - AC-EXECUTORS-PLUGIN-ADMISSION-002.4
  - AC-EXECUTORS-PLUGIN-ADMISSION-002.5
  - AC-EXECUTORS-PLUGIN-ADMISSION-002.6
  - AC-EXECUTORS-PLUGIN-ADMISSION-002.7
  - AC-EXECUTORS-PLUGIN-ADMISSION-002.8
  - AC-EXECUTORS-PLUGIN-ADMISSION-002.9
  - AC-EXECUTORS-PLUGIN-ADMISSION-002.10
system_design:
  - ../../specs/executors/system-design/remote-executor-plugin-environment-listing.md
---

# Task 18: Host environment listing

## Summary

Add the `environment_listing` registry row, the `Host.ListExecutorEnvironments` callback, the paged store query, the phase-to-state mapping, the rate limiter, and the fixture provider's use of it.

## Size

S: ~250 source, ~450 test lines.

## In scope

- Registry row and manifest dependency test; additive proto and SDK method; handler and optional store interface.
- Store query on SQLite and PostgreSQL ordered by environment reference with a strict-greater cursor.
- Fixture provider reads the listing before it places and refuses a launch that would exceed its fixture capacity.

## Out of scope

- Any write, lock, or reservation; a per-node host claim; an administrator view.

## Acceptance

- A provider with the capability pages its own records in byte order, once each, with the mapped state.
- A provider without it gets `unsupported` with zero store reads.

## Failing-first tests

- Cursor paging with a record inserted between pages (SQLite and PostgreSQL).
- Foreign installation and unknown provider key.
- Phase mapping table covering every lifecycle phase constant.
- Over-budget page, store error, rate limit with a fake clock.
- The listing never delays a concurrent launch (blocking-store unit test).

## Verification

Run from the repository root:

```bash
(cd apps/backend && go test -race -trimpath ./internal/plugins/... ./cmd/plugin-fixture -count=1)
```

```bash
(cd apps/backend && go test -race -trimpath ./internal/task/repository/sqlite -run 'EnvironmentListing' -count=1)
```

## Likely files and risks

- `apps/backend/internal/plugins/hostcaps/` (registry row and golden file)
- `pluginsdk/proto/kandev/plugin/v1/plugin.proto`
- `apps/backend/internal/task/repository/sqlite/` (listing query)
- Risk: the registry golden file and retired-identity test change in the same commit.
