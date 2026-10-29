---
created: 2026-10-29
status: draft
requirements:
  - REQ-EXECUTORS-PLUGIN-ADMISSION-001
  - REQ-EXECUTORS-PLUGIN-ADMISSION-002
  - REQ-EXECUTORS-PLUGIN-PLACEMENT-001
  - REQ-EXECUTORS-PLUGIN-PLACEMENT-002
  - REQ-EXECUTORS-PLUGIN-OPS-001
  - REQ-EXECUTORS-PLUGIN-LAUNCHPOLICY-001
  - REQ-EXECUTORS-PLUGIN-PREPARATION-001
system_design:
  - ../../specs/executors/system-design/remote-executor-plugin-admission.md
  - ../../specs/executors/system-design/remote-executor-plugin-environment-listing.md
  - ../../specs/executors/system-design/remote-executor-plugin-operations-migration.md
legacy_specs: []
---

# Implementation plan: native worker slices 2 and 3 (delta)

## Overview

Slices 2 and 3 of the [native worker plan](../executor-plugin-native-workers/plan.md) are already specified and broken into
work orders 10 to 14 there. This plan does not restate them. It records (1) the slice boundaries and what makes each
separately mergeable, (2) one added requirement from the plugin's gap list, (3) the cut items and the owner decisions that keep
them cut, and (4) the open questions. Build starts only after the owner approves this spec and slice 1 has passed live testing
and maintainer feedback.

Owner decisions of 2026-10-29 for this card: keep every earlier cut (resource class, labels, M1, M8); keep the
`REQ-EXECUTORS-PLUGIN-*` ID family (the plugin roadmap cites those IDs; slice 1 alone uses `NATIVE-WORKER`); deliver a delta,
leaving the reviewed drafts untouched; add plugin gap 6 as a requirement and carry gaps 9 and 10 as maintainer questions.

## Slices

| Slice | Content | Work orders | Capabilities (all opt-in) | Depends on |
| --- | --- | --- | --- | --- |
| 2 | Admission and placement | 10 (M2), 11 (M3), 18 (listing, new), 19 (gate) | `admission_context`, `pending_placement`, `provision_refusal`, `environment_listing` | slice 1 merged |
| 3 | Operations and launch policy | 12 (M4), 13 (M5), 14 (M6), 20 (gate) | `provider_operations`, `provider_maintenance`, `launch_policy`, `preparation_hints` | slice 1 merged; not slice 2 |

Slice 3 does not depend on slice 2: the registry dependency closure (`provider_maintenance` needs `provider_operations`;
`launch_policy` needs `workspace_descriptor`; `preparation_hints` needs `repository_intent`) never names a slice 2 capability, and
no slice 3 requirement or design cites a placement or admission phase, field, or type.

The slice-1 branch plan states `depends_on` chains that do contradict this: its task-12 depends on 11, task-13 on 12, task-14 on
13 and 07, so a scheduler reading those files would block slice 3 behind slice 2. That plan is owned by another card and is not
edited here. For Build, this table of effective dependencies is authoritative and overrides the slice-1 plan files for work orders 12, 13, and 14:

| Work order | Effective `depends_on` | Slice-1 plan file says |
| --- | --- | --- |
| 10 | 08 | 08 |
| 11 | 10 | 10 |
| 18 | 11 | (new) |
| 19 (slice 2 gate) | 18 | (new) |
| 12 | 08 | 11 |
| 13 | 12 | 12 |
| 14 | 13, 07 | 13, 07 |
| 20 (slice 3 gate) | 14 | (new) |

Only the 12 to 11 edge is overridden; 13 and 14 reach 08 through 12. When the slice-1 plan is next edited its task-12 should drop
`"11"` for `"08"`; until then this table is the record of the deviation (open question Q-M6 asks the maintainer to confirm).
Either slice then merges upstream alone, in either order, provided slice 1 is merged.

## Work orders

| Order | Work order | Slice | Size |
| --- | --- | --- | --- |
| 18 | [Host environment listing](task-01-environment-listing.md) (ADMISSION-002) | 2 | S: ~300 source, ~500 test lines. |
| 19 | [Slice 2 merge gate](task-02-slice2-merge-gate.md) | 2 | S: ~40 source, ~250 test lines. |
| 20 | [Slice 3 merge gate](task-03-slice3-merge-gate.md) | 3 | S: ~40 source, ~250 test lines. |

Numbers 18 to 20 continue the numbering of the earlier plan (the files are named task-01 to task-03 in this directory). The two
gates are separate orders so neither slice's gate waits for the other slice.

## Shared surfaces between the slices

- **Envelope budget.** The 1536-byte row that holds placement, admission, drain, and the `launch_policy` outcome belongs to the `hostFieldBudget` table introduced
  by slice 1, which already has an entry for every field of both slices. Each slice's worst-case envelope test fills its own fields to the
  maximum and every other field of that row at the maximum recorded in the table, so neither test depends on the other slice's code being present.
- **Proto field numbers.** The listing adds new messages only. Where a work order adds a field to an existing message it
  states the number in the work order; a duplicate number in one message is a protobuf compile error on rebase, the contract is unreleased, and the second slice to merge renumbers its field.
  The merge-gate tests (19 and 20) compile the proto with only their slice's work orders applied.
- **Launch order.** The profile's launch-policy check (slice 3, `validateLaunch`) runs before `admitLaunch` (slice 2), so a policy refusal
  never holds a reservation. A launch-policy outcome failure reported after allocation is an ordinary launch failure and releases the reservation through the existing launch-failure cleanup of slice 2. With only one slice present the other step is absent and the order is unaffected.

## Interleavings

Existing coverage (see the admission and operations designs): queued launch cancelled while placement completes
(PLACEMENT-001.5, .14, barriers `before_placement_commit` and `after_cancel_mark`); admission hook racing a resume
(ADMISSION-001.9, .11); archive, reset, or delete during a queued launch (PLACEMENT-001.13, .16). Added by this delta:
listing racing a launch or a settle (environment listing design). Not applicable: two owners releasing a shared environment,
because M1 is cut (follow-up card 99beef6d-530e-4afd-949b-1424fc9245df, which must decide the reuse candidate set first).

## Lock order

No new lock. The listing takes none. Slice 2 keeps slice 1's order (task row, then descriptors, then the inventory lock, with
the admission scope advisory lock taken inside the operation-creating transaction); slice 3 adds no database lock (the
maintenance idempotency map and the event limiter are in memory).

## Plugin gap mapping

| Plugin gap | Disposition |
| --- | --- |
| 2 profile on attach, inspect, destroy | Closed in slice 1 (the proto carries `profile` on all of them). |
| 6 admission inventory | New ADMISSION-002 (`environment_listing`). |
| 9 host identity or local-node proof | Open question Q-M1 below. |
| 10 readmit RPC | Covered by OPS-001.6 `readmit_node`. |
| 10 richer inspect states, `expected_runtime_identity` | Open question Q-M2 below. |
| 7 receipt binding of fence owner | Plugin decision; no core change. |

## Open questions

For the owner:

- Q-O1: `environment_listing` returns provider state only on request (`include_state`); a page is cut by a 256 KiB size budget
  rather than by a smaller entry count. Is the provider state wanted at all, or is the reference plus listed state enough for the
  Nomad plugin? (Dropping it removes one request field and the size-cut rule.)
- Q-O2: the rate limit (burst 10, 10 per second per installation), the 100-entry page, the 256 KiB response bound, and the 5 second read bound are first values. Confirm or replace.
- Q-O5: the listing is callable only inside a host-admitted provider operation (during Provision). Does the Nomad plugin ever need it outside one (a timer)? If yes, that is a new capability, not a change to this one.
- Q-O3: the `readmit_node` operation is how R6 and C6 readmission land. Confirm that no separate readmit RPC is wanted.
- Q-O4: M1, M8, resource class and labels stay cut. Confirm the plugin's first release does not need an applied resource class
  from the provider (the Nomad plugin roadmap lists no use of one).

For the Kandev maintainer:

- Q-M1: should the host supply a host identity (or a local-node proof) to providers, for a plugin that must prove it runs on the
  backend's own machine? Today the plugin reads a file the user places; gap 9.
- Q-M2: richer inspect states and the meaning of `expected_runtime_identity` on inspect (gap 10): in or out of slice 3?
- Q-M3: is a registry row per capability acceptable for `environment_listing`, or would the maintainer prefer to fold the read
  into `provider_operations` (slice 3), at the cost of tying slice 2 to slice 3?
- Q-M4: `executor.plugin_event` is a new notification event type outside every default subscription (OPS-001.4). Is that
  acceptable, or should events use an existing type?
- Q-M5: is a 600 second fixed placement deadline acceptable now that the provider cannot report one (PLACEMENT-001.2)?
- Q-M6: may the slice-1 plan's task-12 `depends_on` change from 11 to 08, so slice 3 does not wait on slice 2? Is an optional `ExecutorEnvironmentLister` interface in `pluginsdk` acceptable instead of a method on `ExecutorProviderHostAPI`?

## Out of scope

Autoscaling, per-person Nomad quotas, fleet UI beyond the M4 status read, hostile-workload isolation, the mirror itself, and the
cut items above. No production code in this card.

## Risks

- Slice 1 is still changing on PR 4934 and its fixup card; line references in this delta can drift. Re-read the slice 1 head
  before Build.
- A new registry row after slice 1 ships changes `TestRegistryHoldsTheClosedNameSetAtVersionOne`, which pins the 16 names of
  AC-EXECUTORS-PLUGIN-CAPABILITY-001.2; it is updated to 17 names in the same change (`testdata/retired.golden` lists retired pairs only and does not change).
- Both SQL dialects: the listing runs one `executors_running` scan and orders in Go, so the same statement runs on SQLite and PostgreSQL; the store test still runs on both.

## Verification strategy

Each work order lists its commands. User-visible surfaces are unchanged from the earlier plan (pending-placement status and
failure copy, Playwright in work orders 11 and 16); the listing and the merge gates have no UI.
