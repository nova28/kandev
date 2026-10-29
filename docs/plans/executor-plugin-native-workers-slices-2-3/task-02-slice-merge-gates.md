---
id: "19-slice-merge-gates"
title: "Slice 2 and slice 3 standalone merge gates"
status: pending
wave: 9
depends_on:
  - "18"
plan: "plan.md"
requirements:
  - REQ-EXECUTORS-PLUGIN-ADMISSION-001
  - REQ-EXECUTORS-PLUGIN-OPS-001
acceptance_criteria:
  - AC-EXECUTORS-PLUGIN-ADMISSION-001.6
  - AC-EXECUTORS-PLUGIN-OPS-001.8
system_design:
  - ../../specs/executors/system-design/remote-executor-plugin-admission.md
  - ../../specs/executors/system-design/remote-executor-plugin-operations-migration.md
---

# Task 19: Slice merge gates

## Summary

Prove that each of slice 2 and slice 3 builds, passes, and behaves unchanged for a provider that declares none of the other slice's capabilities, so either merges upstream first.

## Size

S: ~80 source, ~400 test lines.

## In scope

- A fixture manifest per slice that declares only that slice's capabilities, plus a conformance test that launches, stops, and destroys it.
- A test that a provider declaring no capability keeps today's synchronous launch, deadline, and behavior with both slices present.
- A check that the registry dependency closure names no slice 2 capability in a slice 3 row, and the reverse.

## Out of scope

- The cross-capability proof and public documentation (work order 16).

## Acceptance

- Slice 3 tests pass on a tree with the slice 2 work orders absent, and slice 2 tests pass with slice 3 absent.

## Failing-first tests

- Dependency closure test across the registry.
- No-capability provider conformance with both slices compiled in.

## Verification

Run from the repository root:

```bash
(cd apps/backend && go test -race -trimpath ./internal/plugins/hostcaps/... ./internal/plugins/... -count=1)
```

## Likely files and risks

- `apps/backend/internal/plugins/hostcaps/`
- `apps/backend/cmd/plugin-fixture`
- Risk: a slice 3 work order quietly using a slice 2 type; the closure test catches it.
