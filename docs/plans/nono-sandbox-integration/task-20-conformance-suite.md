---
id: "20-conformance-suite"
title: "Build the sandbox conformance suite and release gate"
status: pending
wave: 12
depends_on:
  - "05-nono-provider-lifecycle"
  - "07-mcp-connectivity"
  - "09-acceptance-delivery"
plan: "plan.md"
requirements:
  - REQ-AGENTS-SANDBOX-012
acceptance_criteria:
  - AC-AGENTS-SANDBOX-012.3
  - AC-AGENTS-SANDBOX-012.4
  - AC-AGENTS-SANDBOX-012.5
  - AC-AGENTS-SANDBOX-012.7
  - AC-AGENTS-SANDBOX-012.10
  - AC-AGENTS-SANDBOX-012.13
system_design:
  - ../../specs/agents/system-design/sandbox-session-conformance.md
---

# Task 20: Build the sandbox conformance suite and release gate

## Summary

Implement `cmd/sandbox-conformance`: the scripted operator journey with a real agent, paired canaries for every named escape, the matrix, the runner workflow and the result record.

## Scope

- Journey driver and fixture generator with generated marker names.
- Canaries for every escape id in the design, each with a control and the `contained`/`escaped`/`invalid` result.
- Self-hosted runner workflow (manual and scheduled, never on forks) and JSON artifact plus acceptance line.
- Redaction: only marker ids, probe ids and outcomes are printed.

## Acceptance

Each listed criterion in `docs/specs/agents/requirements/sandbox-session-conformance.md` holds as written. A reason code in a refusal is asserted by name in a test.

## Verification

- Suite self-tests with a fake agent: a canary whose control fails is `invalid` and fails the run; a deliberately weakened policy makes the matching canary `escaped`.
- One full real run per cell of the promotion matrix, recorded in `acceptance.md`.

## Files likely touched

- `apps/backend/cmd/sandbox-conformance/`
- `.github/workflows/sandbox-conformance.yml`
- `docs/plans/nono-sandbox-integration/acceptance.md`

## Dependencies

Depends on: 05-nono-provider-lifecycle, 07-mcp-connectivity, 09-acceptance-delivery. See the dependency table in the system design.

## Risks

Needs real agent credentials on runners; a run without a real agent must be labelled `partial` and cannot promote.

## Results

Pending. Design only; no implementation is authorized for this work order yet.
