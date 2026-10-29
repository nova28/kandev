---
id: "20-slice3-merge-gate"
title: "Slice 3 standalone merge gate"
status: pending
wave: 9
depends_on:
  - "14"
plan: "plan.md"
requirements:
  - REQ-EXECUTORS-PLUGIN-OPS-001
acceptance_criteria:
  - AC-EXECUTORS-PLUGIN-OPS-001.8
system_design:
  - ../../specs/executors/system-design/remote-executor-plugin-operations-migration.md
---

# Task 20: Slice 3 merge gate

## Summary

Prove that slice 3 (work orders 12, 13, 14) builds, passes, and behaves unchanged on a tree without work orders 10, 11, 18, and 19, so it merges upstream with slice 2 absent. This is why the effective dependency of work order 12 is 08 and not 11 (plan, Slices).

## Size

S: ~40 source, ~250 test lines.

## In scope

- A fixture manifest that declares only slice 3 capabilities (`provider_operations`, `provider_maintenance`, `launch_policy`, `preparation_hints`) plus their slice 1 prerequisites, and a conformance test that launches, stops, destroys, and reads status.
- A test that a provider declaring no capability keeps today's behavior with slice 3 present.
- A check that no slice 3 registry row, proto message, or Go package imports or requires a slice 2 capability or type (`admission_context`, `pending_placement`, `provision_refusal`, `environment_listing`, placement phases), and that the proto compiles with only slice 1 and slice 3 work orders applied.
- A test that the launch-policy check runs before `admitLaunch` when both are present, and that a launch-policy refusal holds no reservation.

## Out of scope

- The slice 2 gate (work order 19); the cross-capability proof and public documentation (work order 16).

## Acceptance

- Slice 3 tests pass on a tree with the slice 2 work orders absent.

## Failing-first tests

- Dependency closure test: no slice 3 row requires a slice 2 name.
- No-capability provider conformance with slice 3 compiled in.

## Verification

Run from the repository root:

```bash
(cd apps/backend && go test -race -trimpath ./internal/plugins/hostcaps/... ./internal/plugins/... -count=1)
```

## Likely files and risks

- `apps/backend/internal/plugins/hostcaps/`
- `apps/backend/cmd/plugin-fixture`
- Risk: a slice 3 work order quietly using a slice 2 type; the closure and compile checks catch it.
