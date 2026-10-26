---
id: "17-executor-rest-auth-loopback"
title: "Enforce executor REST auth coverage and loopback bind"
status: pending
wave: 10
depends_on:
  - "04-policy-authority"
plan: "plan.md"
requirements:
  - REQ-AGENTS-SANDBOX-012
acceptance_criteria:
  - AC-AGENTS-SANDBOX-012.6
  - AC-AGENTS-SANDBOX-012.16
system_design:
  - ../../specs/agents/system-design/sandbox-session-conformance.md
---

# Task 17: Enforce executor REST auth coverage and loopback bind

## Summary

Prove every agentctl REST route is authenticated or on the fixed exemption list, report the real bind in `/identity`, default sandboxed Local and Worktree instances to loopback, and refuse a sandboxed attempt on an exposed instance. Part A (agentctl only) has no prerequisite; Part B (admission refusal) needs Task 04.

## Scope

- Part A: route-enumeration test against the exemption list on the existing `instanceAuth`; identical 401 body and constant-time comparison; `/identity` fields `bind` and `rest_auth`; launcher loopback default behind `features.agentctlLoopbackBind` (an explicit `agent.standaloneHost` wins); code-server loopback bind for sandboxed attempts.
- Part B: `sandbox_bind_exposed` refusal (reasons `bind_all`, `bind_unreported`) in `prepareExecution`.

## Acceptance

Each listed criterion in `docs/specs/agents/requirements/sandbox-session-conformance.md` holds as written. A reason code in a refusal is asserted by name in a test.

## Verification

- Unit and integration: 401 for missing, wrong, malformed and stale credentials on every non-exempt route; `bind` computed from the real listener; refusal on exposed and unreported instances.
- Canaries `G2_rest_no_credential` and `G2_nonloopback_bind` are implemented in Task 23.

## Files likely touched

- `apps/backend/internal/agentctl/server/api/server.go`
- `apps/backend/internal/agentctl/server/process/vscode.go`
- `apps/backend/internal/agent/runtime/agentctl/launcher/launcher.go`
- `apps/backend/internal/backendapp/sandbox_runtime.go`

## Dependencies

Depends on: 04-policy-authority. See the dependency table in the plan.

## Risks

Existing remote and container executors reach agentctl from outside the host; their bind stays unchanged.

## Results

Pending. Design only; no implementation is authorized for this work order yet.
