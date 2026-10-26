---
id: "19-degraded-states"
title: "Refuse and report instead of failing silently"
status: pending
wave: 10
depends_on:
  - "04-policy-authority"
  - "10-execution-surfaces"
plan: "plan.md"
requirements:
  - REQ-AGENTS-SANDBOX-012
acceptance_criteria:
  - AC-AGENTS-SANDBOX-012.1
  - AC-AGENTS-SANDBOX-012.2
  - AC-AGENTS-SANDBOX-012.12
  - AC-AGENTS-SANDBOX-012.14
system_design:
  - ../../specs/agents/system-design/sandbox-session-conformance.md
---

# Task 19: Refuse and report instead of failing silently

## Summary

Replace the backend abort and the `unknown` launch failure with typed refusals, bounded exit causes and a per-attempt summary.

## Scope

- Typed reason from `newSandboxRuntime`; backend keeps serving; refusing admission installed.
- Pre-handshake exit diagnosis with the closed cause set and redacted 2 KiB tail.
- `sandbox_summary` in the execution snapshot and its rendering.
- Executor capability table; remote types `sandbox_unsupported`.
- Operator-trap fixes found in the journey: explicit errors for org-scoped versus workspace-scoped policies and bindings, and a default policy list.
- Flag `features.sandboxSessionSummary`, off by default.

## Acceptance

Each listed criterion in `docs/specs/agents/requirements/sandbox-session-conformance.md` holds as written. A reason code in a refusal is asserted by name in a test.

## Verification

- Unit: data directory under a temporary root or a symlink returns `state_root_denied` and the backend starts; each exit cause maps as specified; stderr text never stored.
- Integration: Settings shows the reason; a refused launch creates a retryable failed attempt.
- Frontend tests for summary states on desktop and phone.

## Files likely touched

- `apps/backend/internal/backendapp/main.go`
- `apps/backend/internal/backendapp/sandbox_runtime.go`
- `apps/backend/internal/agent/sandbox/`
- `apps/web/components/task/`

## Dependencies

Depends on: 04-policy-authority, 10-execution-surfaces. See the dependency table in the system design.

## Risks

Stderr handling is a leak risk; only the mapped cause may leave agentctl.

## Results

Pending. Design only; no implementation is authorized for this work order yet.
