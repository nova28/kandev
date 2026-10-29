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
  - REQ-EXECUTORS-PLUGIN-PLACEMENT-001
  - REQ-EXECUTORS-PLUGIN-PLACEMENT-002
acceptance_criteria:
  - AC-EXECUTORS-PLUGIN-ADMISSION-001.6
  - AC-EXECUTORS-PLUGIN-PLACEMENT-001.8
  - AC-EXECUTORS-PLUGIN-PLACEMENT-002.5
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
- The launch-order test `TestLaunchPolicyRefusalHoldsNoReservation` and the two trees: see Acceptance. Ownership follows tree contents, not the time the gate ran.
- A test that the codes slice 2 emits are all localized (the exhaustive-code test of work order 11 enumerates only emitters present on the tree; the two `launch_policy_*` codes are work order 13's, plan, Slices).

## Out of scope

- The slice 3 gate (work order 19); the cross-capability proof and public documentation (work order 16).

## Acceptance

- Always: this slice's tests pass on tree (a), the slice-only tree (this slice's branch off the slice-1 head, with no commit of slice 3).
- Launch-order ownership is decided by tree contents. Before this slice merges, rebase its branch onto current main and rerun this gate. If main then contains the other slice (tree (b) = this slice's branch rebased onto that main), `TestLaunchPolicyRefusalHoldsNoReservation` (plan, Shared surfaces, Launch order) must exist in the lifecycle package and pass on tree (b); this slice's PR adds it when it is absent. If main does not contain the other slice, tree (b) does not exist and is not required, and the other slice's gate carries the test when it merges second. Passing this gate before the other slice lands is therefore not a waiver: the rebase-and-rerun is a merge precondition.

## Tests (regression guards unless stated)

- Regression guard, green from the start because the slice-1 registry already holds the final `Requires` rows (it fails if a later change adds a cross-slice requirement): dependency closure test: no slice 2 row requires a slice 3 name.
- Regression guard (green from the start): no-capability provider conformance with slice 2 compiled in.

## Verification

Run from the repository root.

```bash
(cd apps/backend && go test -race -trimpath ./internal/plugins/... ./internal/agent/runtime/lifecycle/... ./cmd/plugin-fixture/... ./pkg/pluginsdk/... -count=1)
(cd apps/web && pnpm run i18n:check && pnpm run typecheck)
make -C pluginsdk proto && git diff --exit-code -- pluginsdk
make -C pluginsdk test
(cd apps/backend && test -n "$KANDEV_TEST_POSTGRES_DSN" && go test -race -trimpath ./internal/agent/runtime/lifecycle/... -count=1)
```

"Slice 2 tests" means the Go packages above (the packages work orders 10 and 11 list), the web `i18n:check` and `typecheck` of work order 11, and the PostgreSQL race tests of work order 11, which skip silently without `KANDEV_TEST_POSTGRES_DSN`: the gate fails when that variable is empty. The Playwright spec of work order 11 is owned by work order 11's own verification, not repeated here.

The proto source is `pluginsdk/proto/kandev/plugin/v1/plugin.proto`; the `git diff --exit-code` after `make -C pluginsdk proto` proves the committed generated code matches it and that the slice's added fields compile without a duplicate number. Building the trees: tree (a) is this slice's branch, built off the slice-1 head and never stacked on the other slice (plan, Slices, Build layout); tree (b) is this slice's branch rebased onto a main that contains the other slice. The commands run on each tree that exists when the gate is rerun after rebase.

## Likely files and risks

- `apps/backend/internal/plugins/hostcaps/`
- `apps/backend/cmd/plugin-fixture`
- Risk: a slice 2 work order quietly using a slice 3 type; the closure and compile checks catch it.
