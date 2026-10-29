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
  - REQ-EXECUTORS-PLUGIN-LAUNCHPOLICY-001
acceptance_criteria:
  - AC-EXECUTORS-PLUGIN-OPS-001.8
  - AC-EXECUTORS-PLUGIN-LAUNCHPOLICY-001.2
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
- The launch-order test `TestLaunchPolicyRefusalHoldsNoReservation` and the two trees: see Acceptance. Ownership follows tree contents, not the time the gate ran.
- Gate 19 asserts the codes `launch_policy_unsupported` and `launch_policy_unenforced` are emitted and carried in the session failure record; their localized copy is work order 11's and is not asserted here (plan, Slices).
- Provider events (OPS-001.3, OPS-001.4): work order 12 owns the `executors:pluginEvent.<kind>` catalog keys for the eight event kinds in all seven locales and the client-side localization of the `executor.plugin_event` payload, which does not use work order 11's failure-copy surface. Gate 19 runs the web checks and a rendering test named `pluginEvent localizes every kind` in `apps/web/lib/plugin-event-copy.test.ts` (work order 12 creates it) that fails when any kind lacks a key or an unknown code does not fall back to the kind's generic text.
- A provider declaring no capability, with a profile that carries no `launch_policy` key, behaves as today; a profile carrying a `launch_policy` other than `none` on such a provider is refused with `launch_policy_unsupported` and no allocation (LAUNCHPOLICY-001.2), which is the specified behavior and not a regression.

## Out of scope

- The slice 2 gate (work order 18); the cross-capability proof and public documentation (work order 16).

## Acceptance

- Always: this slice's tests pass on tree (a), the slice-only tree (this slice's branch off the slice-1 head, with no commit of slice 2).
- Launch-order ownership is decided by tree contents. Before this slice merges, rebase its branch onto current main and rerun this gate. If main then contains the other slice (tree (b) = this slice's branch rebased onto that main), `TestLaunchPolicyRefusalHoldsNoReservation` (plan, Shared surfaces, Launch order) must exist in the lifecycle package and pass on tree (b); this slice's PR adds it when it is absent. If main does not contain the other slice, tree (b) does not exist and is not required, and the other slice's gate carries the test when it merges second. Passing this gate before the other slice lands is therefore not a waiver: the rebase-and-rerun is a merge precondition. The last command of Verification is the presence check of the plan (Shared surfaces, Launch order); it exits non-zero when both markers are defined and the test is missing or does not pass.

## Tests (regression guards unless stated)

- Regression guard, green from the start because the slice-1 registry already holds the final `Requires` rows (it fails if a later change adds a cross-slice requirement): dependency closure test: no slice 3 row requires a slice 2 name.
- Regression guard (green from the start): no-capability provider conformance with slice 3 compiled in.

## Verification

Run from the repository root.

```bash
(cd apps/backend && go test -race -trimpath ./internal/plugins/... ./internal/agent/runtime/lifecycle/... ./cmd/plugin-fixture/... ./internal/notifications/... ./internal/common/launchwrapper/... ./internal/agentctl/server/process/... -count=1)
(cd apps/web && pnpm run i18n:check && pnpm run typecheck && pnpm exec vitest run lib/plugin-event-copy.test.ts --reporter=json | jq -e '[.testResults[].assertionResults[] | select(.title == "pluginEvent localizes every kind" and .status == "passed")] | length >= 1')
make -C pluginsdk proto && git diff --exit-code -- pluginsdk
make -C pluginsdk test
(cd apps/backend/internal/agent/runtime/lifecycle && if grep -Eq 'func .*admitLaunch\(' $(ls *.go | grep -v _test.go) && grep -Eq 'func .*checkLaunchPolicy\(|launch_policy_unenforced' $(ls *.go | grep -v _test.go); then go test -race -trimpath -run '^TestLaunchPolicyRefusalHoldsNoReservation$' -v -count=1 . | grep -q -- '^--- PASS: TestLaunchPolicyRefusalHoldsNoReservation'; fi)
```

"Slice 3 tests" means the Go packages above, which are the union of the packages that work orders 12, 13, and 14 list; slice 3 has no PostgreSQL verification. The vitest line fails unless that exact test ran and passed: a name filter alone exits 0 when nothing matches, so the line names the file and asserts a passed result with `jq -e`. Its web verification is limited to the provider-event copy of work order 12 (above); localized launch-policy failure copy belongs to work order 11 and is not asserted here. The same commands also cover compiling the packages of the notification and agentctl code, so a hidden dependency on a slice 2 type fails the build on tree (a).

The proto source is `pluginsdk/proto/kandev/plugin/v1/plugin.proto`; the `git diff --exit-code` after `make -C pluginsdk proto` proves the committed generated code matches it and that the slice's added fields compile without a duplicate number. Building the trees: tree (a) is this slice's branch, built off the slice-1 head and never stacked on the other slice (plan, Slices, Build layout); tree (b) is this slice's branch rebased onto a main that contains the other slice. The commands run on each tree that exists when the gate is rerun after rebase.

## Likely files and risks

- `apps/backend/internal/plugins/hostcaps/`
- `apps/backend/cmd/plugin-fixture`
- Risk: a slice 3 work order quietly using a slice 2 type; the closure and compile checks catch it.
