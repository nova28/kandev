---
id: "19-slice3-merge-gate"
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

# Task 19: Slice 3 merge gate

## Summary

Prove that slice 3 (work orders 12, 13, 14) builds, passes, and behaves unchanged on a tree without work orders 10, 11, and 18, so it merges upstream with slice 2 absent. This is why the effective dependency of work order 12 is 08 and not 11 (plan, Slices).

## Size

S: ~40 source, ~250 test lines.

## In scope

- A fixture manifest that declares only slice 3 capabilities (`provider_operations`, `provider_maintenance`, `launch_policy`, `preparation_hints`) plus their slice 1 prerequisites, and a conformance test that launches, stops, destroys, and reads status.
- A test that a provider declaring no capability keeps today's behavior with slice 3 present.
- A check that no slice 3 registry row, proto message, or Go package imports or requires a slice 2 capability or type (`admission_context`, `pending_placement`, `provision_refusal`, placement phases), and that the proto compiles with only slice 1 and slice 3 work orders applied.
- The launch-order test (launch-policy check before `admitLaunch`, a policy refusal holds no reservation) is carried by whichever of work orders 18 and 19 runs on a tree where both slices are present: if slice 2 merges first, this gate (work order 19) carries it on the merged tree; if slice 3 merges first, work order 18 carries it. It is not part of this gate's slice-only run.
- The gate's two trees: (a) the slice-only run, on the slice branch with slice 1 merged and the slice 2 work-order commits absent; (b) when slice 2 is already on main, the run on the merged tree, which adds the launch-order test above.

## Out of scope

- The slice 2 gate (work order 18); the cross-capability proof and public documentation (work order 16).

## Acceptance

- Always: slice 3 tests pass on tree (a), the slice-only tree.
- Only when slice 2 is already on main when this slice merges: the same tests, including the launch-order test, also pass on tree (b). When slice 3 merges first, tree (b) does not exist yet and is not required; work order 18 then carries the launch-order test when slice 2 merges, so neither gate can block the first merge.

## Failing-first tests

- Dependency closure test: no slice 3 row requires a slice 2 name.
- No-capability provider conformance with slice 3 compiled in.

## Verification

Run from the repository root. The gate runs the packages that hold its tests: the registry, the plugin host, the lifecycle package (no-capability launch tests), and the fixture provider.

```bash
(cd apps/backend && go test -race -trimpath ./internal/plugins/hostcaps/... ./internal/plugins/... ./internal/agent/runtime/lifecycle/... ./cmd/plugin-fixture/... -count=1)
```

Building the trees: tree (a) is a branch off main with slice 1 merged that carries only this slice's work-order commits (the other slice's commits are not cherry-picked); tree (b) is main after the other slice has merged. The run above is repeated on each tree that exists at gate time.

## Likely files and risks

- `apps/backend/internal/plugins/hostcaps/`
- `apps/backend/cmd/plugin-fixture`
- Risk: a slice 3 work order quietly using a slice 2 type; the closure and compile checks catch it.
