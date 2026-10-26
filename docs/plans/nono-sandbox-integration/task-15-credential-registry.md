---
id: "15-credential-registry"
title: "Declare per-agent credential requirements"
status: pending
wave: 10
depends_on:
  - "05-nono-provider-lifecycle"
  - "24-summary-foundation"
plan: "plan.md"
requirements:
  - REQ-AGENTS-SANDBOX-012
acceptance_criteria:
  - AC-AGENTS-SANDBOX-012.11
system_design:
  - ../../specs/agents/system-design/sandbox-session-conformance.md
---

# Task 15: Declare per-agent credential requirements

## Summary

Add the agent credential registry and the admission check that refuses a sandboxed launch whose agent credential cannot reach the child, and supply the diagnosis hints used by exit diagnosis.

## Scope

- Registry in `internal/agent/sandboxruntime` keyed by agent type and platform: methods `{kind, delivery}` plus `missing_markers`; entries for `claude-acp`, `codex-acp` and the e2e mock agent (method `none`), so a sandboxed mock launch is not refused `sandbox_credentials_unknown`.
- Evaluation in `prepareExecution` after environment resolution, with `file_in_agent_state` read from the session's existing agent state only; check producing `sandbox_credentials_unsupported`, `sandbox_credentials_missing` and `sandbox_credentials_unknown` with the any-one-method rule.
- Setup hint text for the refusal (names and paths only, localized in seven locales).
- Build test rejecting an `env_key` that names an injected or core minimal variable. Seed provisioning (a Kandev-owned seed directory and copy into agent state) is Out of scope; the remedy names the env key.

## Acceptance

Each listed criterion in `docs/specs/agents/requirements/sandbox-session-conformance.md` holds as written. A reason code in a refusal is asserted by name in a test.

## Verification

- Unit: every kind on linux and darwin; any-one-method rule; empty env value is unsatisfied; absent or empty agent_state file is unsatisfied; unknown agent refuses; keychain-only agent refuses on darwin; values never appear in errors, logs or reports.
- Real run: Claude Code and Codex on each host; measured entries recorded in `acceptance.md` before they ship.

## Files likely touched

- `apps/backend/internal/agent/sandboxruntime/credentials.go`
- `apps/backend/internal/backendapp/sandbox_launch.go`
- `apps/web/src/locales/*/agents.json`

## Dependencies

Depends on: 05-nono-provider-lifecycle, 24-summary-foundation. See the dependency table in the plan.

## Risks

A wrong registry entry would block a working agent or admit one that cannot log in. Entries are provisional until measured on a real host.

## Results

Pending. Design only; no implementation is authorized for this work order yet.
