---
id: "18-process-surface-labels"
title: "Sandbox or label every session process"
status: pending
wave: 11
depends_on:
  - "05-nono-provider-lifecycle"
  - "10-execution-surfaces"
plan: "plan.md"
requirements:
  - REQ-AGENTS-SANDBOX-012
acceptance_criteria:
  - AC-AGENTS-SANDBOX-012.8
system_design:
  - ../../specs/agents/system-design/sandbox-session-conformance.md
---

# Task 18: Sandbox or label every session process

## Summary

Route every process Kandev starts for a session through one entry that sandboxes it or records an `unsandboxed` label before it starts.

## Scope

- `SandboxProcessKind` in `internal/agent/sandbox` mapped from `streams.ProcessKind` plus `language_server`, `terminal`, `code_server`, `helper`.
- One start entry in lifecycle, scriptengine and agentctl process management; label written before the start; `process_label_unrecorded` on write failure.
- Labels in the launch snapshot and Execution details; first delivery labels scripts `unsandboxed`; terminals follow the darwin refusal.

## Acceptance

Each listed criterion in `docs/specs/agents/requirements/sandbox-session-conformance.md` holds as written. A reason code in a refusal is asserted by name in a test.

## Verification

- Unit: each kind is sandboxed or labelled; unknown state is labelled; label recorded before output; write failure aborts the start; shared processes carry the starting attempt's label.
- Go-AST test fails on a process start outside the entry and an allowlist.
- Frontend: Execution details shows the label on desktop and phone in seven locales.

## Files likely touched

- `apps/backend/internal/agent/runtime/lifecycle/`
- `apps/backend/internal/scriptengine/providers.go`
- `apps/backend/internal/agent/sandbox/`
- `apps/web/components/task/session-execution-details.tsx`

## Dependencies

Depends on: 05-nono-provider-lifecycle, 10-execution-surfaces. See the dependency table in the plan.

## Risks

Finding every start site is the risk; the AST test is part of the work.

## Results

Pending. Design only; no implementation is authorized for this work order yet.
