---
id: "16-preview-origin"
title: "Serve previews from a separate origin"
status: cancelled
wave: 10
depends_on:
  - "24-summary-foundation"
plan: "plan.md"
requirements:
  - REQ-AGENTS-SANDBOX-012
acceptance_criteria: []
system_design:
  - ../../specs/agents/system-design/sandbox-session-conformance.md
---

# Task 16: Serve previews from a separate origin

## Summary

Serve sandboxed-attempt previews (HTML preview and session port proxy) from a per-task origin distinct from the UI, or disable the preview with a reason.

## Scope

- Per-task origin `<token>.<preview.baseDomain>` and cookie isolation in the browser-facing proxy (`gateway/websocket/port_proxy.go`) and the publish handler (`agentctl/server/api/workspace_preview.go`).
- New config key `preview.baseDomain`; assertions that it differs from and is not a parent of the UI host and that UI cookies are host-only.
- Isolation for any task with a guard or any sandboxed attempt ever; table `task_preview_origins` with a lazily created 256-bit token; `POST /api/v1/tasks/:id/preview-ticket` single-use ticket (60 s) exchanged for a host-only cookie; 403 and 503 `preview_origin_unavailable` and 404 on a deleted task, for HTTP and WebSocket.
- `preview_origin_unavailable` state and UI message; flag `features.previewSeparateOrigin` (registered by Task 24, off by default); ADR `2026-10-26-sandboxed-preview-origin`.
- Transition barrier (durable "guard or any attempt" test on every request and upgrade, socket registry, post-commit close), route shape `/port-proxy/:sessionId/:port/*path` under the origin, auth-off gate by the task token, and the ticket and preview-session lifecycle (digest storage, delete-returning single use, deletion on logout or user change) of the companion design.

## Acceptance

Each listed criterion in `docs/specs/agents/requirements/sandbox-session-conformance.md` holds as written. A reason code in a refusal is asserted by name in a test.

## Verification

- Unit: origin differs from the UI origin; origins differ across tasks; token stable for a task and invalid after deletion; redirects and WebSockets stay on the preview origin.
- Browser test on desktop and phone: a preview page cannot read UI cookies or storage; disabled state shows its reason.

## Files likely touched

- `apps/backend/internal/gateway/websocket/port_proxy.go`
- `apps/backend/internal/agentctl/server/api/workspace_preview.go`
- `apps/backend/internal/common/config/catalog.go`
- `apps/web/components/task/`

## Dependencies

Depends on: 24-summary-foundation (flags and summary types).

## Risks

Wildcard names are not available on every deployment; the disabled state must be the fallback, never the UI origin.

## Results

Cancelled in this package. The separate preview origin moved to Out of scope in the requirement; 012.9 is an honest `degraded` `preview_same_origin` label (Tasks 18 and 24) until a follow-up card delivers the origin. The ADR `2026-10-26-sandboxed-preview-origin` is deferred.
