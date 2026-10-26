---
id: "23-canary-set-and-promotion"
title: "Complete the canary set and the promotion record"
status: pending
wave: 14
depends_on:
  - "11-guarded-session-surface"
  - "12-session-token-broker-relay"
  - "13-managed-git-isolation"
  - "15-credential-registry"
  - "17-executor-rest-auth-loopback"
  - "18-process-surface-labels"
  - "19-degraded-states"
  - "20-conformance-suite"
  - "21-standalone-integration"
plan: "plan.md"
requirements:
  - REQ-AGENTS-SANDBOX-012
acceptance_criteria:
  - AC-AGENTS-SANDBOX-012.1
  - AC-AGENTS-SANDBOX-012.2
  - AC-AGENTS-SANDBOX-012.3
  - AC-AGENTS-SANDBOX-012.4
  - AC-AGENTS-SANDBOX-012.5
  - AC-AGENTS-SANDBOX-012.6
  - AC-AGENTS-SANDBOX-012.7
  - AC-AGENTS-SANDBOX-012.8
  - AC-AGENTS-SANDBOX-012.9
  - AC-AGENTS-SANDBOX-012.10
  - AC-AGENTS-SANDBOX-012.11
  - AC-AGENTS-SANDBOX-012.12
  - AC-AGENTS-SANDBOX-012.13
  - AC-AGENTS-SANDBOX-012.16
system_design:
  - ../../specs/agents/system-design/sandbox-session-conformance.md
---

# Task 23: Complete the canary set and the promotion record

## Summary

Implement every canary in the design table on the Task 20 framework and run the promotion matrix.

## Scope

- All canaries of the design table with their controls, run with the suite's flag set (all three flags on, environment overrides). `profiles.yaml` is not in `conformance-scope.txt`; the promotion commit adds the artifacts and `acceptance.md` lines and flips the prod flags, and `--verify-release` passes at that commit.
- One full run per required promotion cell, recorded in `acceptance.md` with `--acceptance-line`.
- Runtime flag promotion changes for the two new flags once `--verify-release` passes.
- Promotion is one pull request setting `prod: true` for `features.sandboxPolicies` and the two new flags, whose required check runs `--verify-release` on its head; the `G1_pin_policy_change` control uses an operator-started launch after a binding change.

## Acceptance

Each listed criterion in `docs/specs/agents/requirements/sandbox-session-conformance.md` holds as written. A reason code in a refusal is asserted by name in a test.

## Verification

- Each canary is `contained` with a valid control on a real host; weakening the matching mechanism makes it `escaped`.
- `--verify-release` passes for the six required cells at the release commit.

## Files likely touched

- `apps/backend/cmd/sandbox-conformance/`
- `docs/plans/nono-sandbox-integration/acceptance.md`
- `profiles.yaml`

## Dependencies

Depends on: 11-guarded-session-surface, 12-session-token-broker-relay, 13-managed-git-isolation, 15-credential-registry, 17-executor-rest-auth-loopback, 18-process-surface-labels, 19-degraded-states, 20-conformance-suite, 21-standalone-integration. See the dependency table in the plan.

## Risks

Hosts and agent credentials must be available for every required cell; any missing cell blocks promotion.

## Results

Pending. Design only; no implementation is authorized for this work order yet.
