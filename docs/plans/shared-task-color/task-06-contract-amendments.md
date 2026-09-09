---
id: "06-contract-amendments"
title: "Amend the sidebar task-colours contract"
status: pending
wave: 5
depends_on: ["05-color-menu-writes-task"]
plan: "plan.md"
spec: "../../specs/shared-task-color/spec.md"
acceptance_criteria: []
---

# Task 06: Amend the sidebar task-colours contract

## Summary

Land the four named amendments to the shipped sidebar task-colours requirement
and system-design documents, so the frozen contract matches the behaviour this
package delivers. These are the only permitted edits to those two files.

## In scope

- **Terminology / "Effective color"** gains the third source: first matching
  automatic colour; else the manual colour when the viewer holds one; else the
  task colour.
- **AC-UI-SIDEBAR-AUTOMATIC-TASK-COLORS-002.8** becomes "… its manual color,
  otherwise its task color, or no marker."
- **AC-UI-SIDEBAR-AUTOMATIC-TASK-COLORS-002.10** is retained verbatim, with a
  note recording that it constrains rule evaluation only and not the colour
  menu.
- **System design / Purpose and boundaries** — "never writes task records" is
  scoped to rule evaluation, and the manual-colour write path is recorded as
  targeting `PATCH /api/v1/tasks/:id`.

## Out of scope

- Any other edit to those two documents.
- Changing `docs/specs/shared-task-color/spec.md` itself.

## Acceptance

- All four passages read as specified, and AC-…-002.10's original sentence is
  still present word for word.
- Specification linting passes.

## Verification

```bash
python3 scripts/lint-spec-files.py --all
```

## Files likely touched

- `docs/specs/ui/requirements/sidebar-automatic-task-colors.md`
- `docs/specs/ui/system-design/sidebar-automatic-task-colors.md`

## Dependencies

Task 05 — the amendments describe behaviour that only becomes true once the menu
writes the task record.

## Risks

- Rewriting AC-…-002.10 instead of annotating it would drop the guarantee that
  rule evaluation never mutates a task, which AC-16 still depends on.
- Editing passages beyond the four named ones exceeds the permission the spec
  grants over these frozen documents.

## Parallelism

`parallel-safe` with Task 07 — `docs/` versus `apps/web/e2e`, no shared files.

## Inputs

- Spec section *Amendments to the sidebar task-colours contract*.
- `docs/specs/ui/requirements/sidebar-automatic-task-colors.md` and its
  system-design sibling.

## Results

Pending.
