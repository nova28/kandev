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

Serve sandboxed-session previews from a per-task origin distinct from the UI, or disable the preview with a reason.

## Scope

- Per-task preview origin and cookie isolation in the preview proxy (`agentctl/server/api/workspace_preview.go` and backend routing).
- `preview_origin_unavailable` state and UI message.
- Flag `features.previewSeparateOrigin`, off by default.

## Acceptance

Each listed criterion in `docs/specs/agents/requirements/sandbox-session-conformance.md` holds as written. A reason code in a refusal is asserted by name in a test.

## Verification

- Unit: origin differs from the UI origin; origins differ across tasks; the UI cookie is not sent to a preview.
- Browser test on desktop and phone: a preview page cannot read UI storage; disabled state shows its reason.

## Files likely touched

- `apps/backend/internal/agentctl/server/api/workspace_preview.go`
- `apps/backend/internal/runtimeflags/registry.go`
- `profiles.yaml`
- `apps/web/components/task/`

## Dependencies

None on #4769 work orders.

## Risks

Wildcard names are not available on every deployment; the disabled state must be the fallback, never the UI origin.

## Results

Pending. Design only; no implementation is authorized for this work order yet.
