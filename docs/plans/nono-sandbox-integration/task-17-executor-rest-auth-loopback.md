---
id: "17-executor-rest-auth-loopback"
title: "Authenticate executor REST routes and offer loopback bind"
status: pending
wave: 10
depends_on: []
plan: "plan.md"
requirements:
  - REQ-AGENTS-SANDBOX-012
acceptance_criteria:
  - AC-AGENTS-SANDBOX-012.6
system_design:
  - ../../specs/agents/system-design/sandbox-session-conformance.md
---

# Task 17: Authenticate executor REST routes and offer loopback bind

## Summary

Require the instance credential on agentctl REST routes, add a loopback bind setting, and refuse a sandboxed attempt on an exposed instance.

## Scope

- Auth middleware on agentctl routes using the existing instance credential.
- Bind setting (`loopback`, `all`) and the instance report of its bind.
- Admission refusal `sandbox_bind_exposed`.
- Flag `features.agentctlLoopbackBind`, off by default.

## Acceptance

Each listed criterion in `docs/specs/agents/requirements/sandbox-session-conformance.md` holds as written. A reason code in a refusal is asserted by name in a test.

## Verification

- Unit and integration: 401 without the credential on every route group; loopback bind unreachable from a non-loopback address; admission refusal on `all`.
- Canaries `G2_rest_no_credential` and `G2_nonloopback_bind` in Task 20.

## Files likely touched

- `apps/backend/internal/agentctl/server/api/server.go`
- `apps/backend/internal/common/config/catalog.go`
- `apps/backend/internal/backendapp/sandbox_launch.go`

## Dependencies

None on #4769 work orders.

## Risks

Existing remote and container executors reach agentctl from outside the host; the bind default must stay `all` for them.

## Results

Pending. Design only; no implementation is authorized for this work order yet.
