---
id: "19-degraded-states"
title: "Refuse and report instead of failing silently"
status: pending
wave: 11
depends_on:
  - "04-policy-authority"
  - "10-execution-surfaces"
  - "15-credential-registry"
  - "17-executor-rest-auth-loopback"
  - "24-summary-foundation"
plan: "plan.md"
requirements:
  - REQ-AGENTS-SANDBOX-012
acceptance_criteria:
  - AC-AGENTS-SANDBOX-012.1
  - AC-AGENTS-SANDBOX-012.2
  - AC-AGENTS-SANDBOX-012.3
  - AC-AGENTS-SANDBOX-012.6
  - AC-AGENTS-SANDBOX-012.10
  - AC-AGENTS-SANDBOX-012.12
  - AC-AGENTS-SANDBOX-012.14
  - AC-AGENTS-SANDBOX-012.16
system_design:
  - ../../specs/agents/system-design/sandbox-session-conformance.md
---

# Task 19: Refuse and report instead of failing silently

## Summary

Replace the backend abort and the `unknown` launch failure with typed refusals, bounded exit causes and a per-attempt summary.

## Scope

- Typed reason from `newSandboxRuntime`; refusing runtime that keeps the backend serving; `reason_code` added to availability; provider conditions re-evaluated per read.
- Exit diagnosis in agentctl with the closed cause set, core-supplied hints, redacted 2 KiB tail; `launch_cause` persisted and rendered.
- Plug the 012.1, 012.12, 012.16 and `rest_auth` (012.6) checks into the Task 24 admission registry in the requirement's evaluation order; populate summary items and events; render the indicator matrix; name-only environment reporting with `missing`.
- Re-admission on new-process resume, recovery and replacement (012.3), writing a refusal record on refusal; continuation runs no admission.
- Executor capability table: only Local and Worktree supported; Settings availability gains `executors`.
- The flag and the refusal table come from Task 24; the 012.1 keep-serving fix is not gated.
- Export the core minimal and injected environment lists as one source and add `environment.shadowed`; define `SystemReadRoots` per host OS for `executable_readable`; run the `executor_unsupported` part of 012.12 before instance creation; read `/identity` once per launch (5 s) shared by 012.16, 012.6 and the 011.2 host test; refusal cleanup order (revoke token, delete instance, log failures); refusal-record item states and the reasons `rest_auth_none` and `token_unsupported`.

## Acceptance

Each listed criterion in `docs/specs/agents/requirements/sandbox-session-conformance.md` holds as written. A reason code in a refusal is asserted by name in a test.

## Verification

- Unit: data directory under a temporary root or symlink returns `state_root_denied` and the backend starts; installing the provider clears `provider_missing` without restart; each exit cause maps with precedence; stderr text never leaves agentctl; unsupported executor refuses.
- Integration: Settings shows the reason; a refused launch writes a `sandbox_launch_refusals` row, creates no attempt, and is retryable; refusal still returns if the write fails.
- Frontend tests for summary states, shield and warning indicator on desktop and phone.

## Files likely touched

- `apps/backend/internal/backendapp/main.go`
- `apps/backend/internal/backendapp/sandbox_runtime.go`
- `apps/backend/internal/agent/sandbox/`
- `apps/backend/internal/agentctl/`
- `apps/web/components/task/`

## Dependencies

Depends on: 04-policy-authority, 10-execution-surfaces, 15-credential-registry, 17-executor-rest-auth-loopback, 24-summary-foundation. See the dependency table in the plan.

## Risks

Stderr handling is a leak risk; only the mapped cause may leave agentctl. Operator-trap fixes found in the journey (scope errors, default policy list) are not in this package; see the plan.

## Results

Pending. Design only; no implementation is authorized for this work order yet.
