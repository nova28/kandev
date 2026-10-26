---
id: "18-process-surface-labels"
title: "Sandbox or label every session process"
status: pending
wave: 11
depends_on:
  - "05-nono-provider-lifecycle"
  - "10-execution-surfaces"
  - "24-summary-foundation"
plan: "plan.md"
requirements:
  - REQ-AGENTS-SANDBOX-012
acceptance_criteria:
  - AC-AGENTS-SANDBOX-012.8
  - AC-AGENTS-SANDBOX-012.9
system_design:
  - ../../specs/agents/system-design/sandbox-session-conformance.md
---

# Task 18: Sandbox or label every session process

## Summary

Route every process Kandev starts for a session through one entry that sandboxes it or records an `unsandboxed` label before it starts, and record the first preview served for an attempt before any preview byte is sent.

## Scope

- `SandboxProcessKind` in `internal/agent/sandbox` with kinds `setup_script`, `cleanup_script`, `custom_script`, `dev_process`, `language_server`, `agent_terminal`, `user_terminal`, `code_server`, `helper`, passed explicitly at every call site and never inferred from `streams.ProcessKind`.
- One start entry in lifecycle, scriptengine and agentctl process management; label written `pending` before the start, then `started` or `failed`; `process_label_unrecorded` on write failure.
- Preview state (012.9): column `preview_served_at` on the attempt, committed before the first preview byte, by the firing sites `apps/backend/internal/task/handlers/workspace_preview_handlers.go` (HTML preview publish) and `apps/backend/internal/gateway/websocket/port_proxy.go` (session port proxy); `preview_state_changed` fired from the commit; a failed commit refuses the request with 503 `preview_state_unrecorded` and serves nothing; concurrent first requests serialize on the commit; a request with no live attempt is served and recorded for no attempt; nothing is recorded while `features.sandboxSessionSummary` is off. Task 24 owns the event handling and the item.
- Labels in the launch snapshot and Execution details; only `agent_terminal` is sandboxed (refused on darwin), every other kind is `unsandboxed`, and `helper` does not degrade 012.8.

## Acceptance

Each listed criterion in `docs/specs/agents/requirements/sandbox-session-conformance.md` holds as written. A reason code in a refusal is asserted by name in a test.

## Verification

- Unit: each kind is sandboxed or labelled; unknown state is labelled; label recorded before output; write failure aborts the start; shared processes carry the starting attempt's label.
- Go-AST test fails on a process start outside the entry and an allowlist.
- Preview state: first publish and first port-proxy request each commit the state before the first byte; two concurrent first requests commit once; a failed commit returns 503 `preview_state_unrecorded` with no body served and the next request retries; no-attempt request records nothing; flag off records nothing; a replacement attempt starts at `holds`.
- Frontend: Execution details shows the label on desktop and phone in seven locales.

## Files likely touched

- `apps/backend/internal/agent/runtime/lifecycle/`
- `apps/backend/internal/scriptengine/providers.go`
- `apps/backend/internal/task/handlers/workspace_preview_handlers.go`
- `apps/backend/internal/gateway/websocket/port_proxy.go`
- `apps/backend/internal/agent/sandbox/`
- `apps/web/components/task/session-execution-details.tsx`

## Dependencies

Depends on: 05-nono-provider-lifecycle, 10-execution-surfaces, 24-summary-foundation. See the dependency table in the plan.

## Risks

Finding every start site is the risk; the AST test is part of the work.

## Results

Pending. Design only; no implementation is authorized for this work order yet.
