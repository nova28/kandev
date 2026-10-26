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

When the standalone MCP session token change and the standalone managed Git isolation change (REQ-AGENTS-GITISOLATION) land on main, reconcile this branch with their final names, flags and specs.

## Scope

- Reconcile requirement ids, flags and spec files with the merged standalone versions.
- The standalone token change (kdlbs/kandev#4798, head aced58fb73) registers `features.sessionMcpToken` (`KANDEV_FEATURES_SESSION_MCP_TOKEN`, off in every profile, restart required) and advertises `session_mcp_token` plus `mcp_token_fingerprint` on `/identity` (REQ-EXECUTORS-SESSION-CONTROL-ISOLATION-001.11, 004.1); with it off nothing is minted and AC-012.5 reads `unknown` `token_unsupported`. Reconcile the 012.5 item with that flag, and its `mcp_token_*` launch codes with the 012.1 refusal path.
- Remove duplicated code from Tasks 12 and 13 that the standalone changes now own.
- Update the journey steps and the canary mapping to the final names.

- Export the broker environment list (managed credential keys and Git configuration entries) from `githubauth` as the single source the child environment injects (Task 19 reads it).
- Write the attempt columns `guard_enforced` (guard present and the server-side class table registered for the session's MCP endpoint) and `git_isolation_active` at admission for items 012.4 and 012.7.

## External merge gates

Build of this work order waits for the standalone managed Git isolation change and the standalone MCP session token change to open and merge. When their PRs open, re-read their specs and update this work order and the plan.

## Acceptance

Each listed criterion in `docs/specs/agents/requirements/sandbox-session-conformance.md` holds as written. A reason code in a refusal is asserted by name in a test.

## Verification

- Both standalone test suites pass on the combined branch.
- The canaries themselves are implemented and verified in Task 23.

## Files likely touched

- `apps/backend/internal/mcp/`
- `apps/backend/internal/common/subproc/`
- `docs/specs/agents/`

## Dependencies

Depends on: 12-session-token-broker-relay, 13-managed-git-isolation. See the dependency table in the plan.

## Risks

Blocked until both standalone changes are merged; their shape may differ from Tasks 12 and 13, so this work order must be re-read before starting.

## Results

Pending. Design only; no implementation is authorized for this work order yet.
