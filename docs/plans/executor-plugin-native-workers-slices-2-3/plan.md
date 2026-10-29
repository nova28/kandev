---
created: 2026-10-29
status: draft
requirements:
  - REQ-EXECUTORS-PLUGIN-ADMISSION-001
  - REQ-EXECUTORS-PLUGIN-PLACEMENT-001
  - REQ-EXECUTORS-PLUGIN-PLACEMENT-002
  - REQ-EXECUTORS-PLUGIN-OPS-001
  - REQ-EXECUTORS-PLUGIN-LAUNCHPOLICY-001
  - REQ-EXECUTORS-PLUGIN-PREPARATION-001
system_design:
  - ../../specs/executors/system-design/remote-executor-plugin-admission.md
  - ../../specs/executors/system-design/remote-executor-plugin-operations-migration.md
legacy_specs: []
---

# Implementation plan: native worker slices 2 and 3 (delta)

## Overview

Slices 2 and 3 of the [native worker plan](../executor-plugin-native-workers/plan.md) are already specified and broken into
work orders 10 to 14 there. This plan does not restate them. It records (1) the slice boundaries and what makes each
separately mergeable, (2) the two merge gates added for separate mergeability, (3) the cut items and the owner decisions that keep
them cut, and (4) the open questions. Build starts only after the owner approves this spec and slice 1 has passed live testing
and maintainer feedback.

Owner decisions of 2026-10-29 for this card: keep every earlier cut (resource class, labels, M1, M8); keep the
`REQ-EXECUTORS-PLUGIN-*` ID family (the plugin roadmap cites those IDs; slice 1 alone uses `NATIVE-WORKER`); deliver a delta,
leaving the reviewed drafts untouched; carry plugin gaps 9 and 10 as maintainer questions. Round 3 of Spec Review (owner decision, 2026-10-29): the host environment listing (plugin gap 6, former work order 18) is cut from slice 2 and recorded under Out of scope.

## Slices

| Slice | Content | Work orders | Capabilities (all opt-in) | Depends on |
| --- | --- | --- | --- | --- |
| 2 | Admission and placement | 10 (M2), 11 (M3), 18 (gate) | `admission_context`, `pending_placement`, `provision_refusal` | slice 1 merged |
| 3 | Operations and launch policy | 12 (M4), 13 (M5), 14 (M6), 19 (gate) | `provider_operations`, `provider_maintenance`, `launch_policy`, `preparation_hints` | slice 1 merged; not slice 2 |

Slice 3 does not depend on slice 2: the registry dependency closure (`provider_maintenance` needs `provider_operations`;
`launch_policy` needs `workspace_descriptor`; `preparation_hints` needs `repository_intent`) never names a slice 2 capability, and
no slice 3 requirement or design cites a placement or admission phase, field, or type.

Localized failure copy stays with work order 11 (slice 2), as AC PLACEMENT-001.9 and task-11 already state: work order 11 builds the status surface
and the `executors:pluginFailure` catalog keys for every host failure code of that AC, including the two slice 3 codes
`launch_policy_unsupported` and `launch_policy_unenforced`, and its own tests and work order 16's parametrized Playwright spec prove them on the merged tree.
Gate 18 therefore adds no localization test and gate 19 asserts the codes only, not their copy. A slice-3-only tree has no status surface for those codes;
their localized text appears when slice 2 merges, which is accepted (Out of scope, Localized text on a slice-3-only tree).

Provider-event copy is different: the `executors:pluginEvent.<kind>` keys and the client localization of `executor.plugin_event` (OPS-001.3, .4) belong to work order 12 (slice 3), because the web client localizes the notification payload on its own and needs nothing from work order 11's status surface. The slice-1 task-12 file does not list web files; this delta states the ownership and gate 19 verifies it (open question Q-M9).

The slice-1 branch plan states `depends_on` chains that do contradict this: its task-12 depends on 11, task-13 on 12, task-14 on
13 and 07, so a scheduler reading those files would block slice 3 behind slice 2. That plan is owned by another card and is not
edited here. For Build, this table of effective dependencies is authoritative and overrides the slice-1 plan files for work orders 12, 13, 14, and 16:

| Work order | Effective `depends_on` | Slice-1 plan file says |
| --- | --- | --- |
| 10 | 08 | 08 |
| 11 | 10 | 10 |
| 18 (slice 2 gate) | 11 | (new) |
| 12 | 08 | 11 |
| 13 | 12 | 12 |
| 14 | 13, 07 | 13, 07 |
| 16 (cross-capability proof) | 11, 14, 17 | 17 in its task file; 14, 17 in the slice-1 plan table |
| 19 (slice 3 gate) | 14 | (new) |

Only the 12 to 11 edge is overridden in the chain of 12 to 14; 13 and 14 reach 08 through 12. Work order 16 proves slice 2 behavior
(ADMISSION-001.9, PLACEMENT-001.5, .9, .14) and used to reach work order 11 only through 14, 13, 12; with that edge cut, 11 is added
to its dependencies explicitly. The gates 18 and 19 are merge proofs, not code that 16 consumes, so 16 does not depend on them.
The `wave` column of the slice-1 plan is also overridden: waves order work within one slice and do not place slice 3 after slice 2.

Build layout: each slice is built on its own branch off the slice-1 head (slice 2 carries 10 and 11 and gate 18; slice 3 carries 12,
13, 14 and gate 19), never stacked on the other, so tree (a) of each gate is that branch itself. Work order 16 is built after both
branches are on main. If a branch cannot be built without the other slice's code, the gate fails and the dependency is a spec defect
to route back to Spec, not a reason to stack. When the slice-1 plan is next edited its task-12 should drop
`"11"` for `"08"`; until then this table is the record of the deviation (open question Q-M6 asks the maintainer to confirm).
Either slice then merges upstream alone, in either order, provided slice 1 is merged.

### Capability window while one slice is merged

Slice 1's registry already names all seven slice-2 and slice-3 capabilities (`admission_context`, `pending_placement`, `provision_refusal`, `provider_operations`, `provider_maintenance`, `launch_policy`, `preparation_hints`) at version 1, so on a tree with only one slice merged a provider that requires the other slice's capability passes the registry and echo gates and nothing enforces it. This is an accepted, recorded window, not a behaviour either gate asserts: the contract is unreleased (the registry constant `firstRelease` is 0.99.0), so no installed provider can depend on it. The window also exists on a tree with slice 1 alone, where all seven rows are unimplemented, and the argument expires when 0.99.0 ships: Q-M7 must be answered before any release that contains slice 1 without both slices 2 and 3. No rule in this delta closes it: removing the unmerged slice's rows before a one-slice release would contradict AC-EXECUTORS-PLUGIN-CAPABILITY-001.2, which pins the closed 16-name set, and would edit the slice-1 registry test that the slice-1 card owns. Whether to remove the rows or mark them unavailable until their slice merges is a slice-1 contract decision, asked as open question Q-M7.

## Work orders

| Order | Work order | Slice | Size |
| --- | --- | --- | --- |
| 18 | [Slice 2 merge gate](task-01-slice2-merge-gate.md) | 2 | S: ~40 source, ~250 test lines. |
| 19 | [Slice 3 merge gate](task-02-slice3-merge-gate.md) | 3 | S: ~40 source, ~250 test lines. |

Numbers 18 and 19 continue the numbering of the earlier plan (the files are named task-01 and task-02 in this directory). The two
gates are separate orders so neither slice's gate waits for the other slice. Commits b029e75c6d and earlier on this branch used number
18 for the environment listing work order that has since been cut; from 4d703386ee on, 18 is the slice 2 gate and the cut order is
only a history reference (Out of scope, Host environment listing).

## Shared surfaces between the slices

- **Envelope budget.** The `coordination` group of `hostFieldBudget` (`apps/backend/internal/agent/runtime/lifecycle/executor_plugin_state_budget.go`)
  holds `phase`, `placement`, `admission`, `launch_policy`, and the slice-1 fields in one 1536-byte group maximum. The table records a group maximum, not a maximum per field.
  Each slice's worst-case envelope test fills its own keys with the longest values their ACs permit, and fills every key of the group that its tree does not own with a
  fixed-size filler string whose size is chosen so that the group total is exactly 1536 bytes, then asserts the marshaled group is at most 1536. The filler stands in for
  the other slice's fields, so neither test needs the other slice's Go types; the merged tree runs the same test with real values for all keys.
- **Proto field numbers and the proto check.** The proto source is `pluginsdk/proto/kandev/plugin/v1/plugin.proto`, a separate Go module generated by `make -C pluginsdk proto`
  (the `apps/backend/proto/...` paths in work orders 10, 11, 12, 14 are stale and mean this file). Where a work order adds a field to an existing message it states the number
  in the work order; a duplicate number in one message is a protobuf compile error on rebase, the contract is unreleased, and the second slice to merge renumbers its field.
  Each gate runs `make -C pluginsdk proto` on its tree followed by `git diff --exit-code -- pluginsdk` (generated code is committed and must match the source) and `make -C pluginsdk test`.
- **Launch order.** The profile's launch-policy check (slice 3, in `validateLaunch`) runs before `admitLaunch` (slice 2), so a policy refusal
  never holds a reservation. With only one slice present the other step is absent and the order is unaffected.
  After allocation, two cases differ. A descriptor weaker than the profile (LAUNCHPOLICY-001.3) is rejected with cleanup: the environment is destroyed, and the reservation is released when
  that destroy confirms absence (ADMISSION-001.5). An agentctl start that reports `failed` or unenforced for `required` (LAUNCHPOLICY-001.4) fails the session with
  `launch_policy_unenforced`, and the environment is retained, not destroyed, so its reservation stays held until the retained environment is later destroyed and absence is confirmed.
  Ownership of the ordering test is decided by tree contents, not by gate time: the test is `TestLaunchPolicyRefusalHoldsNoReservation` in the lifecycle package, and a slice's merge is
  blocked until, on that slice's branch rebased onto current main, it exists whenever both the `validateLaunch` launch-policy step and `admitLaunch` are present, and passes. The slice
  that merges second therefore adds it in its own PR; each gate's task file carries the precondition and the rerun after rebase.
  Work order 13 shall implement its launch-policy step as a function named `checkLaunchPolicy`, called from `validateLaunch`; work order 10 names its gate `admitLaunch` (task-10). Those two
  identifiers are the presence markers of the check below; because the slice-1 task-13 file does not name the function, the check also treats a non-test lifecycle file that emits `launch_policy_unenforced` as the launch-policy marker, so an inline implementation cannot make the check skip. Each gate's Verification runs a presence check that fails when `admitLaunch` and either marker are defined in non-test Go files of the
  lifecycle package and `go test -run '^TestLaunchPolicyRefusalHoldsNoReservation$' -v` does not print `--- PASS`. The same check is a merge precondition on the exact head that merges, so
  the second of two back-to-back slice merges (each rebased onto a main that lacked the other) fails it on its rebased head; merging through the merge queue or one at a time satisfies this (open question Q-M8).

## Interleavings

Existing coverage (see the admission and operations designs): queued launch cancelled while placement completes
(PLACEMENT-001.5, .14, barriers `before_placement_commit` and `after_cancel_mark`); admission hook racing a resume
(ADMISSION-001.9, .11); archive, reset, or delete during a queued launch (PLACEMENT-001.13, .16).
Not applicable: two owners releasing a shared environment,
because M1 is cut (follow-up card 99beef6d-530e-4afd-949b-1424fc9245df, which must decide the reuse candidate set first).

## Lock order

No new lock. Slice 2 keeps slice 1's order (task row, then descriptors, then the inventory lock, with
the admission scope advisory lock taken inside the operation-creating transaction); slice 3 adds no database lock (the
maintenance idempotency map and the event limiter are in memory).

## Plugin gap mapping

| Plugin gap | Disposition |
| --- | --- |
| 2 profile on attach, inspect, destroy | Closed in slice 1 (the proto carries `profile` on all of them). |
| 6 admission inventory | Cut from this delta (Out of scope, Host environment listing); follow-up card `ef83e6ee-307c-4d4a-80a5-d1e875c16577`. |
| 9 host identity or local-node proof | Open question Q-M1 below. |
| 10 readmit RPC | Covered by OPS-001.6 `readmit_node`. |
| 10 richer inspect states, `expected_runtime_identity` | Open question Q-M2 below. |
| 7 receipt binding of fence owner | Plugin decision; no core change. |

## Open questions

For the owner:

- Q-O3: the `readmit_node` operation is how R6 and C6 readmission land. Confirm that no separate readmit RPC is wanted.
- Q-O4: M1, M8, resource class and labels stay cut. Confirm the plugin's first release does not need an applied resource class
  from the provider (the Nomad plugin roadmap lists no use of one).

For the Kandev maintainer:

- Q-M1: should the host supply a host identity (or a local-node proof) to providers, for a plugin that must prove it runs on the
  backend's own machine? Today the plugin reads a file the user places; gap 9.
- Q-M2: richer inspect states and the meaning of `expected_runtime_identity` on inspect (gap 10): in or out of slice 3?
- Q-M4: `executor.plugin_event` is a new notification event type outside every default subscription (OPS-001.4). Is that
  acceptable, or should events use an existing type?
- Q-M5: is a 600 second fixed placement deadline acceptable now that the provider cannot report one (PLACEMENT-001.2)?
- Q-M7: slice 1's registry names the slice 2 and 3 capabilities before they exist; should it mark them unavailable until their slice merges, or may the unmerged slice's rows be removed before the first release, given that AC-EXECUTORS-PLUGIN-CAPABILITY-001.2 pins the 16-name set (plan, Capability window)?
- Q-M8: are the two slice PRs merged through the merge queue (which tests the combined head) or one at a time? The launch-order presence check relies on one of the two.
- Q-M9: may the slice-1 plan's task-12 gain the web files and the `executors:pluginEvent` catalog keys and rendering test that this delta assigns to work order 12 (OPS-001.3, .4), which task-12 does not list today?
- Q-M6: may the slice-1 plan's task-12 `depends_on` change from 11 to 08, so slice 3 does not wait on slice 2?

## Out of scope

Autoscaling, per-person Nomad quotas, fleet UI beyond the M4 status read, hostile-workload isolation, the mirror itself, and the
cut items above. No production code in this card.

### Localized text on a slice-3-only tree, cut in Spec Review round 5

- **Flow cut:** moving the copy keys `executors:pluginFailure.launch_policy_unsupported` and `executors:pluginFailure.launch_policy_unenforced` (and the status surface that renders them) to work order 13 so a slice-3-only tree shows localized launch-policy failures, plus the gate 19 assertion of that text and its `i18n:check` run (former open question Q-O6).
- **Why cut:** the status surface and catalog namespace are built by work order 11 (slice 2); work order 13 lists no web files, so the move needed a second surface that would collide with work order 11's on merge. The owner chose to keep copy with work order 11 (2026-10-29).
- **What a follow-up needs:** a decision on who builds the minimal failure-copy surface and `pluginFailure` namespace without slice 2, a reconcile rule for the second slice to merge, web verification commands and a named rendering test in gate 19, and an edit to task-11's ownership sentence for the slice 1 card.

### Host environment listing (plugin gap 6), cut in Spec Review round 3

- **Flow cut:** a new opt-in capability `environment_listing` (the 17th registry name) and a paged, read-only host callback `ListExecutorEnvironments`, with which a worker-pool provider reads the non-terminal environments the host holds for its own installation and provider key during its launch's Provision operation, so a placement decision counts environments that have no provider-side marker yet. Former requirement REQ-EXECUTORS-PLUGIN-ADMISSION-002 (ACs .1 to .11), former work order 18. The full draft is in the git history of this branch (commit b029e75c6d, files `remote-executor-plugin-environment-listing.md` under `docs/specs/executors/requirements/` and `system-design/`, and task-01).
- **Why cut:** the round-3 review found nine contract defects that need decisions rather than added text (below), and the owner chose to defer the capability rather than keep growing the delta.
- **What a follow-up needs:** card `ef83e6ee-307c-4d4a-80a5-d1e875c16577` (untagged: the workspace tag cap was reached). It must decide: whether the callback is served to every dispatch of the launch operation (Provision, Recover, Destroy, ResolveExecutorConnection share one operation context and registration) or only Provision, and where a discriminator would live (SA-001); the wait a listing imposes on the end of its dispatch through the lease lock (SA-004); the named store accessor and that a missing lister is `unavailable`, not an empty page (SA-005); work order 11 declaring `creating` and the `pluginExecutorPhase` prefix for the exhaustive phase test (SA-006); classification of a caller deadline shorter than the 5 second bound (SA-007); an untestable "cursor not reused" clause (SA-010); where the `unaddressable` count sits relative to the installation and `absent` filters (SA-011); grouping versus dropping `absent` rows (C-01); and the precedence of `unsupported` over `unavailable` (C-02). Also: the `IntroducedIn` rule for a registry row added after the first release, an older host answering gRPC `Unimplemented`, the optional-interface assertion in `grpcHostServer`, and the SDK release record. The former open questions Q-O1 (provider state on request), Q-O2 (rate, page, size and read bounds), Q-O5 (callable outside Provision), Q-M3 (registry row versus folding into `provider_operations`) and the optional `ExecutorEnvironmentLister` interface part of Q-M6 move with it.

## Risks

- Slice 1 is still changing on PR 4934 and its fixup card; line references in this delta can drift. Re-read the slice 1 head
  before Build.
- This delta adds no registry row: the 16-name set pinned by AC-EXECUTORS-PLUGIN-CAPABILITY-001.2 and `TestRegistryHoldsTheClosedNameSetAtVersionOne` is unchanged.

## Verification strategy

Each work order lists its commands. User-visible surfaces are unchanged from the earlier plan (pending-placement status and
failure copy, Playwright in work orders 11 and 16); the merge gates have no UI.
