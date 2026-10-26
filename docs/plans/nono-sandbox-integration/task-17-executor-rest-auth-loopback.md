---
id: "17-executor-rest-auth-loopback"
title: "Authenticate executor REST routes and report the real bind"
status: pending
wave: 10
depends_on:
  - "24-summary-foundation"
plan: "plan.md"
requirements:
  - REQ-AGENTS-SANDBOX-012
acceptance_criteria:
  - AC-AGENTS-SANDBOX-012.6
  - AC-AGENTS-SANDBOX-012.16
system_design:
  - ../../specs/agents/system-design/sandbox-session-conformance.md
---

# Task 17: Authenticate executor REST routes and report the real bind

## Summary

Prove every agentctl REST route is authenticated or on the fixed exemption list, report the real bind and `rest_auth` in the instance `/identity`, and bind the sandboxed code-server to loopback. This work order is agentctl only; the admission refusals (`sandbox_bind_exposed`, `sandbox_rest_auth_unenforced`) are Task 19.

## Scope

- Route-enumeration test for the instance router against the exemption list on the existing `instanceAuth` (cleaned-path matching, `/git-credentials/` subtree for every method), and a second enumeration test holding the control router to its own four exemptions; identical 401 body and constant-time comparison.
- Instance `/identity` fields `bind` (from the instance and control listeners), `rest_auth` and the existing `mcp_token_fingerprint`; the control `/identity` is unchanged.
- No launcher change: `agent.standaloneHost` stays the single dial and listen key. Code-server binds loopback for a sandboxed create request while `features.agentctlLoopbackBind` is on (consulted per request, no restart).

## Acceptance

Each listed criterion in `docs/specs/agents/requirements/sandbox-session-conformance.md` holds as written. A reason code in a refusal is asserted by name in a test.

## Verification

- Unit and integration: 401 for missing, wrong, malformed and stale credentials on every non-exempt route; `bind` computed from the real instance and control listeners; `rest_auth` reported as `enforced` or `none`.
- Canaries `G2_rest_no_credential` and `G2_nonloopback_bind` are implemented in Task 23.

## Files likely touched

- `apps/backend/internal/agentctl/server/api/server.go`
- `apps/backend/internal/agentctl/server/process/vscode.go`
- `apps/backend/internal/agent/runtime/agentctl/launcher/launcher.go`

## Dependencies

Depends on: 24-summary-foundation. See the dependency table in the plan.

## Risks

Existing remote and container executors reach agentctl from outside the host; their bind stays unchanged.

## Results

Pending. Design only; no implementation is authorized for this work order yet.
