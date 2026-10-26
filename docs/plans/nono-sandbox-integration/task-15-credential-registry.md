---
id: "15-credential-registry"
title: "Declare per-agent credential requirements"
status: pending
wave: 10
depends_on:
  - "05-nono-provider-lifecycle"
plan: "plan.md"
requirements:
  - REQ-AGENTS-SANDBOX-012
acceptance_criteria:
  - AC-AGENTS-SANDBOX-012.11
  - AC-AGENTS-SANDBOX-012.10
system_design:
  - ../../specs/agents/system-design/sandbox-session-conformance.md
---

# Task 15: Declare per-agent credential requirements

## Summary

Add the agent credential registry and the admission check that refuses a sandboxed launch whose agent credential cannot reach the child.

## Scope

- Registry in `internal/agent/sandboxruntime` keyed by agent type and platform, with `claude-acp` and `codex-acp` entries.
- Admission check producing `sandbox_credentials_unsupported` and `sandbox_credentials_unknown`.
- Setup hint text for the refusal (names and paths only, localized).

## Acceptance

Each listed criterion in `docs/specs/agents/requirements/sandbox-session-conformance.md` holds as written. A reason code in a refusal is asserted by name in a test.

## Verification

- Unit: every kind on linux and darwin; unknown agent refuses; keychain-only agent refuses on darwin; values never appear in errors, logs or reports.
- Real run: Claude Code and Codex on each host launch with their documented credential path or refuse with the right class (recorded in the conformance suite).

## Files likely touched

- `apps/backend/internal/agent/sandboxruntime/credentials.go`
- `apps/backend/internal/backendapp/sandbox_launch.go`
- `apps/web/src/locales/*/agents.json`

## Dependencies

Depends on: 05-nono-provider-lifecycle. See the dependency table in the system design.

## Risks

A wrong registry entry would block a working agent or admit one that cannot log in. Entries need a real-host measurement before they ship.

## Results

Pending. Design only; no implementation is authorized for this work order yet.
