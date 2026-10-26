---
id: "16-preview-origin"
title: "Serve previews from a separate origin"
status: pending
wave: 10
depends_on: []
plan: "plan.md"
requirements:
  - REQ-AGENTS-SANDBOX-012
acceptance_criteria:
  - AC-AGENTS-SANDBOX-012.9
system_design:
  - ../../specs/agents/system-design/sandbox-session-conformance.md
---

# Task 16: Serve previews from a separate origin

## Summary

Serve sandboxed-attempt previews (HTML preview and session port proxy) from a per-task origin distinct from the UI, or disable the preview with a reason.

## Scope

- Per-task origin `<token>.<preview.baseDomain>` and cookie isolation in the browser-facing proxy (`gateway/websocket/port_proxy.go`) and the publish handler (`agentctl/server/api/workspace_preview.go`).
- New config key `preview.baseDomain`; assertions that it differs from and is not a parent of the UI host and that UI cookies are host-only.
- `preview_origin_unavailable` state and UI message; flag `features.previewSeparateOrigin`, off by default; ADR `2026-10-26-sandboxed-preview-origin`.

## Acceptance

Each listed criterion in `docs/specs/agents/requirements/sandbox-session-conformance.md` holds as written. A reason code in a refusal is asserted by name in a test.

## Verification

- Unit: origin differs from the UI origin; origins differ across tasks; token stable for a task and invalid after deletion; redirects and WebSockets stay on the preview origin.
- Browser test on desktop and phone: a preview page cannot read UI cookies or storage; disabled state shows its reason.

## Files likely touched

- `apps/backend/internal/gateway/websocket/port_proxy.go`
- `apps/backend/internal/agentctl/server/api/workspace_preview.go`
- `apps/backend/internal/common/config/catalog.go`
- `apps/backend/internal/runtimeflags/registry.go`
- `profiles.yaml`
- `apps/web/components/task/`

## Dependencies

None on other work orders.

## Risks

Wildcard names are not available on every deployment; the disabled state must be the fallback, never the UI origin.

## Results

Pending. Design only; no implementation is authorized for this work order yet.
