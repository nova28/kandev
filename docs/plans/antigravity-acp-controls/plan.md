---
spec: docs/specs/agents/antigravity-acp.md
created: 2026-08-15
status: ready
---

# Implementation Plan: Antigravity ACP Agent Controls

## Overview

Extend the local `agy-acp` bridge's ACP session contract, then expose the
bridge's explicit process flags through Kandev's existing agent-profile
mechanisms. The web client should require no provider-specific feature code:
it already renders ACP modes, configuration options, and curated CLI flags.

## Implementation waves and parallel candidates

- [ ] [Task 01: Extend the agy-acp session contract](task-01-bridge-session-controls.md) (`ready`)
- [ ] [Task 02: Expose explicit Antigravity profile switches](task-02-kandev-profile-controls.md) (`blocked by Task 01`)
- [ ] [Task 03: Verify the end-to-end local integration](task-03-contract-and-local-verification.md) (`blocked by Tasks 01-02`)

Task 01 owns the bridge state and ACP payload. Task 02 owns Kandev's catalogue
contract. They are ordered because Task 02 must not advertise bridge flags or
capabilities that the installed bridge cannot parse.

## Risks and guardrails

- Keep `AGY_EXTRA_ARGS` compatible but do not make it the documented control
  plane.
- Never imply that `--sandbox` is stronger than the installed `agy` CLI
  actually guarantees.
- Do not add Antigravity-specific web components or duplicate existing ACP
  capability normalization.
- The Kandev repository and the checked-out `agy-acp` source are distinct git
  repositories. Make separate, focused commits; do not overwrite unrelated
  local changes in either.
- Cost reporting and MCP injection remain explicitly out of scope until the
  upstream CLI exposes a stable, observed contract.
