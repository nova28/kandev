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
| 2 | Admission and placement | 10 (M2), 11 (M3), 18 (listing, new) | `admission_context`, `pending_placement`, `provision_refusal`, `environment_listing` | slice 1 merged |
| 3 | Operations and launch policy | 12 (M4), 13 (M5), 14 (M6) | `provider_operations`, `provider_maintenance`, `launch_policy`, `preparation_hints` | slice 1 merged; not slice 2 |

Slice 3 does not depend on slice 2: the registry dependency closure (`provider_maintenance` needs `provider_operations`;
`launch_policy` needs `workspace_descriptor`; `preparation_hints` needs `repository_intent`) never names a slice 2 capability.
The one cross-slice touch is work order 14, which the existing plan lists as depending on 13 and 07 only, and work order 12,
which the existing plan sequences after 11 for review order only. Both are re-stated here as having no code dependency on
slice 2, so each slice merges upstream alone in either order.

## Work orders

| Order | Work order | Slice | Size |
| --- | --- | --- | --- |
| 18 | [Host environment listing](task-01-environment-listing.md) (ADMISSION-002) | 2 | S: ~250 source, ~450 test lines. |
| 19 | [Slice merge gates](task-02-slice-merge-gates.md) | 2 and 3 | S: ~80 source, ~400 test lines. |

Numbers 18 and 19 continue the numbering of the earlier plan (the files are named task-01 and task-02 in this directory).

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

- Q-O1: `environment_listing` returns provider state only on request (`include_state`) with 20 entries per page. Is the
  provider state wanted at all, or is the reference plus state enough for the Nomad plugin?
- Q-O2: the rate limit of 10 calls per second and the 256 KiB response bound are first values. Confirm or replace.
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

## Out of scope

Autoscaling, per-person Nomad quotas, fleet UI beyond the M4 status read, hostile-workload isolation, the mirror itself, and the
cut items above. No production code in this card.

## Risks

- Slice 1 is still changing on PR 4934 and its fixup card; line references in this delta can drift. Re-read the slice 1 head
  before Build.
- A new registry row after slice 1 ships changes the registry golden file; the retired-identity test must be updated in the
  same change.
- Both SQL dialects: the listing query needs a SQLite and a PostgreSQL test.

## Verification strategy

Each work order lists its commands. User-visible surfaces are unchanged from the earlier plan (pending-placement status and
failure copy, Playwright in work orders 11 and 16); the listing and the merge gates have no UI.
