---
id: "24-summary-foundation"
title: "Add the summary contract, flags and admission registry"
status: pending
wave: 9
depends_on:
  - "04-policy-authority"
  - "10-execution-surfaces"
plan: "plan.md"
requirements:
  - REQ-AGENTS-SANDBOX-012
acceptance_criteria:
  - AC-AGENTS-SANDBOX-012.14
system_design:
  - ../../specs/agents/system-design/sandbox-session-conformance.md
---

# Task 24: Add the summary contract, flags and admission registry

## Summary

Foundation that Tasks 15, 17, 18 and 19 build on: flags, summary types and events, the refusal table, and an ordered admission-check registry.

## Scope

- Register `features.sandboxSessionSummary` and `features.agentctlLoopbackBind` (admission-only, no restart) through the runtime flag registry (off in every profile) with the profile and frontend contract tests.
- Attempt columns `guard_enforced` and `git_isolation_active` (written by Task 21) read by items 012.4 and 012.7. Item, state and reason types, the summary computation and the event contract (`attempt_admitted`, `process_started`, `preview_state_changed`, `exit_diagnosed`, `token_revoked`, `git_policy_changed`, `evidence_lost`, `attempt_ended`).
- Tables `sandbox_teardown_pending` and `sandbox_launch_refusals` with the retention of 20 per session, written only while the summary flag is on, hidden when it is off.
- An ordered admission-check registry in the requirement's evaluation order that Tasks 15 and 19 register checks into.
- Events also include `handshake_completed`, `evidence_restored` and `process_start_failed`; receipt-order sequence numbers assigned at one serialization point; the 10 s `/identity` evidence probe with a per-attempt poll counter, the failed-source set (`identity`, `stream`; `evidence_lost` when a source joins an empty set, `evidence_restored` only when a removal empties it) and the `evidence_gap` latch stored on the attempt row, set at the `evidence_restored` that empties the set for items with an unrecoverable change, or for every runtime item when the summary flag is turned off on a live attempt; `attempt_ended` after the exit report or 5 s, emitted by the startup reconciliation of unadopted attempts too; core records its own token revocation before revoking, and a refusal or attempt-end teardown whose revoke and delete both fail is recorded in table `sandbox_teardown_pending` (`instance_id`, `created_at`, deleted on success) and retried by the executor teardown and startup reconciliation; `preview_state_changed` handled from the commit Task 18 makes before the first preview byte; summary item set is the 13 guarantee items (012.1 to 012.12 and 012.16); the indicator matrix row for `degraded` with any non-failed status.

## Acceptance

Each listed criterion in `docs/specs/agents/requirements/sandbox-session-conformance.md` holds as written. A reason code in a refusal is asserted by name in a test.

## Verification

- Unit: summary derivation for every state combination, the `refused` item rules, event ordering, frozen state after `attempt_ended`, refusal pruning and failed-write fallback, flag registry completeness tests.

## Files likely touched

- `apps/backend/internal/agent/sandbox/`
- `apps/backend/internal/runtimeflags/registry.go`
- `profiles.yaml`

## Dependencies

Depends on: 04-policy-authority, 10-execution-surfaces. See the dependency table in the plan.

## Risks

Task 10 owns the snapshot shape; the summary field is additive to it.

## Results

Pending. Design only; no implementation is authorized for this work order yet.
