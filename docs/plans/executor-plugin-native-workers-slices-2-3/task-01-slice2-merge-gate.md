---
id: "18-slice2-merge-gate"
title: "Slice 2 standalone merge gate"
status: pending
wave: 9
depends_on:
  - "11"
plan: "plan.md"
requirements:
  - REQ-EXECUTORS-PLUGIN-ADMISSION-001
acceptance_criteria:
  - AC-EXECUTORS-PLUGIN-ADMISSION-001.6
system_design:
  - ../../specs/executors/system-design/remote-executor-plugin-admission.md
---

# Task 18: Slice 2 merge gate

## Summary

Prove that slice 2 (work orders 10 and 11) builds, passes, and behaves unchanged for a provider that declares none of slice 3's capabilities, so it merges upstream with slice 3 absent.

## Size

S: ~40 source, ~250 test lines.

## In scope

- A fixture manifest that declares only slice 2 capabilities (`admission_context`, `pending_placement`, `provision_refusal`) plus their slice 1 prerequisites, and a conformance test that launches, stops, and destroys it.
- A test that a provider declaring no capability keeps today's synchronous launch, deadline, and behavior with slice 2 present.
- The launch-order test (launch-policy check before `admitLaunch`, a policy refusal holds no reservation) runs here only when slice 3 is already on main (tree b), otherwise it is carried by work order 19. The gate's two trees: (a) the slice-only run, on the slice branch with slice 1 merged and the slice 3 work-order commits absent; (b) the run on main with slice 3 merged.
- A check that no slice 2 registry row, proto message, or Go package imports or requires a slice 3 capability or type, and that the proto compiles with only slice 1 and slice 2 work orders applied.

## Out of scope

- The slice 3 gate (work order 19); the cross-capability proof and public documentation (work order 16).

## Acceptance

- Always: slice 2 tests pass on tree (a), the slice-only tree.
- Only when slice 3 is already on main when this slice merges: the same tests, including the launch-order test, also pass on tree (b). When slice 2 merges first, tree (b) does not exist yet and is not required; work order 19 then carries the launch-order test when slice 3 merges, so neither gate can block the first merge.

## Failing-first tests

- Dependency closure test: no slice 2 row requires a slice 3 name.
- No-capability provider conformance with slice 2 compiled in.

## Verification

Run from the repository root. The gate runs the packages that hold its tests: the registry, the plugin host, the lifecycle package (no-capability launch tests), and the fixture provider.

```bash
(cd apps/backend && go test -race -trimpath ./internal/plugins/hostcaps/... ./internal/plugins/... ./internal/agent/runtime/lifecycle/... ./cmd/plugin-fixture/... -count=1)
```

Building the trees: tree (a) is a branch off main with slice 1 merged that carries only this slice's work-order commits (the other slice's commits are not cherry-picked); tree (b) is main after the other slice has merged. The run above is repeated on each tree that exists at gate time.

## Likely files and risks

- `apps/backend/internal/plugins/hostcaps/`
- `apps/backend/cmd/plugin-fixture`
- Risk: a slice 2 work order quietly using a slice 3 type; the closure and compile checks catch it.
