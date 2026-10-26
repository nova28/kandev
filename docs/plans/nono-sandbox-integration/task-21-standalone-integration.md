---
id: "21-standalone-integration"
title: "Integrate the extracted token and managed Git pieces"
status: pending
wave: 13
depends_on:
  - "12-session-token-broker-relay"
  - "13-managed-git-isolation"
plan: "plan.md"
requirements:
  - REQ-AGENTS-SANDBOX-012
acceptance_criteria:
  - AC-AGENTS-SANDBOX-012.5
  - AC-AGENTS-SANDBOX-012.7
system_design:
  - ../../specs/agents/system-design/sandbox-session-conformance.md
---

# Task 21: Integrate the extracted token and managed Git pieces

## Summary

When the standalone MCP-token and managed-Git changes land on main, rebase the combined branch onto their final names, flags and specs and re-run their canaries.

## Scope

- Reconcile requirement ids, flags and spec files with the merged standalone versions.
- Remove duplicated code from Tasks 12 and 13 that the standalone changes now own.
- Wire both into the conformance journey and canaries.

## Acceptance

Each listed criterion in `docs/specs/agents/requirements/sandbox-session-conformance.md` holds as written. A reason code in a refusal is asserted by name in a test.

## Verification

- Both standalone test suites pass on the combined branch; canaries `G2_wrong_token` and `G3_repo_*` are `contained` with valid controls.

## Files likely touched

- `apps/backend/internal/mcp/`
- `apps/backend/internal/common/subproc/`
- `docs/specs/agents/`

## Dependencies

Depends on: 12-session-token-broker-relay, 13-managed-git-isolation. See the dependency table in the system design.

## Risks

Blocked until both standalone PRs are merged; the shape may differ from Tasks 12 and 13 and this work order must be re-read before starting.

## Results

Pending. Design only; no implementation is authorized for this work order yet.
