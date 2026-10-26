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

Foundation that Tasks 15, 16, 17, 18 and 19 build on: flags, summary types and events, the refusal table, and an ordered admission-check registry.

## Scope

- Register `features.sandboxSessionSummary`, `features.agentctlLoopbackBind` and `features.previewSeparateOrigin` through the runtime flag registry (off in every profile) with the profile and frontend contract tests.
- Item, state and reason types, the summary computation and the event contract (`attempt_admitted`, `process_started`, `preview_state_changed`, `exit_diagnosed`, `token_revoked`, `git_policy_changed`, `evidence_lost`, `attempt_ended`).
- Table `sandbox_launch_refusals` with the retention of 20 per session, written only while the summary flag is on, hidden when it is off.
- An ordered admission-check registry in the requirement's evaluation order that Tasks 15, 17 and 19 register checks into.

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
