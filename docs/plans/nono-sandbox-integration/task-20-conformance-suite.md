---
id: "20-conformance-suite"
title: "Build the sandbox conformance harness and journey"
status: pending
wave: 12
depends_on:
  - "05-nono-provider-lifecycle"
  - "07-mcp-connectivity"
  - "09-acceptance-delivery"
  - "19-degraded-states"
plan: "plan.md"
requirements:
  - REQ-AGENTS-SANDBOX-012
acceptance_criteria:
  - AC-AGENTS-SANDBOX-012.13
system_design:
  - ../../specs/agents/system-design/sandbox-session-conformance.md
---

# Task 20: Build the sandbox conformance harness and journey

## Summary

Implement `cmd/sandbox-conformance`: the scripted operator journey with a real agent, the canary framework with controls and result states, the runner workflow, the result record, `--acceptance-line` and `--verify-release`.

## Scope

- Journey driver, fixture generator with generated marker names, step bounds and not-applicable reasons from the closed list.
- Canary framework: probe, control, `contained`/`escaped`/`invalid`/`not_applicable`/`error`; wrapping of the 011.11 darwin suite.
- Self-hosted runner workflow (manual and scheduled, never forks, concurrency group per runner, cleanup trap), JSON artifact with `scope_hash`, `conformance-scope.txt`.
- Journey expected summary and indicator after each step, the darwin result mapping with `L_<name>` liveness checks, and the `scope_hash` algorithm of the companion design; the journey asserts effective flags instead of an enable step.

## Acceptance

Each listed criterion in `docs/specs/agents/requirements/sandbox-session-conformance.md` holds as written. A reason code in a refusal is asserted by name in a test.

## Verification

- Suite self-tests with a fake agent: a failing control makes the canary `invalid` and fails the run; a timeout is `error`; a deliberately weakened policy makes the matching canary `escaped`.
- Journey with one real agent on one host, recorded in `acceptance.md`.

## Files likely touched

- `apps/backend/cmd/sandbox-conformance/`
- `.github/workflows/sandbox-conformance.yml`
- `docs/plans/nono-sandbox-integration/conformance-scope.txt`
- `docs/plans/nono-sandbox-integration/acceptance.md`

## Dependencies

Depends on: 05-nono-provider-lifecycle, 07-mcp-connectivity, 09-acceptance-delivery (which itself depends on 02 and 08), 19-degraded-states. See the dependency table in the plan.

## Risks

Needs real agent credentials on runners; a run without a real agent is `partial` and cannot promote.

## Results

Pending. Design only; no implementation is authorized for this work order yet.
