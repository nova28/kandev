---
id: "19-slice2-merge-gate"
title: "Slice 2 standalone merge gate"
status: pending
wave: 9
depends_on:
  - "18"
plan: "plan.md"
requirements:
  - REQ-EXECUTORS-PLUGIN-ADMISSION-001
  - REQ-EXECUTORS-PLUGIN-ADMISSION-002
acceptance_criteria:
  - AC-EXECUTORS-PLUGIN-ADMISSION-001.6
  - AC-EXECUTORS-PLUGIN-ADMISSION-002.10
system_design:
  - ../../specs/executors/system-design/remote-executor-plugin-admission.md
  - ../../specs/executors/system-design/remote-executor-plugin-environment-listing.md
---

# Task 19: Slice 2 merge gate

## Summary

Prove that slice 2 (work orders 10, 11, 18) builds, passes, and behaves unchanged for a provider that declares none of slice 3's capabilities, so it merges upstream with slice 3 absent.

## Size

S: ~40 source, ~250 test lines.

## In scope

- A fixture manifest that declares only slice 2 capabilities (`admission_context`, `pending_placement`, `provision_refusal`, `environment_listing`) plus their slice 1 prerequisites, and a conformance test that launches, stops, and destroys it.
- A test that a provider declaring no capability keeps today's synchronous launch, deadline, and behavior with slice 2 present.
- A check that no slice 2 registry row, proto message, or Go package imports or requires a slice 3 capability or type, and that the proto compiles with only slice 1 and slice 2 work orders applied.

## Out of scope

- The slice 3 gate (work order 20); the cross-capability proof and public documentation (work order 16).

## Acceptance

- Slice 2 tests pass on a tree with the slice 3 work orders absent.

## Failing-first tests

- Dependency closure test: no slice 2 row requires a slice 3 name.
- No-capability provider conformance with slice 2 compiled in.

## Verification

Run from the repository root:

```bash
(cd apps/backend && go test -race -trimpath ./internal/plugins/hostcaps/... ./internal/plugins/... -count=1)
```

## Likely files and risks

- `apps/backend/internal/plugins/hostcaps/`
- `apps/backend/cmd/plugin-fixture`
- Risk: a slice 2 work order quietly using a slice 3 type; the closure and compile checks catch it.
