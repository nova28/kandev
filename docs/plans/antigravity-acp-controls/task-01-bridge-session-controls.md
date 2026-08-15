---
id: "01-bridge-session-controls"
title: "Extend the agy-acp session contract"
status: ready
wave: 1
depends_on: []
plan: "plan.md"
spec: "../../specs/agents/antigravity-acp.md"
parallelism: sequential
---

# Task 01: Extend the agy-acp Session Contract

## Intent

Make the bridge truthfully expose and apply the observed `agy` modes and
effort settings, parse explicit parent-process sandbox/permission flags, and
normalize shell command input at the bridge boundary.

## Acceptance

- ACP session responses include models, `accept-edits`/`plan` modes, and the
  model plus effort select options.
- `session/set_mode` and `session/setMode` validate and persist the selected
  mode; `session/set_config_option` and `session/setConfigOption` validate and
  persist the effort option without breaking model selection.
- Child `agy` argument construction applies mode and effort and maps explicit
  bridge switches to `--dangerously-skip-permissions` and `--sandbox` exactly
  once.
- Legacy persisted session files restore with safe documented defaults.
- `CommandLine`/`Cwd` normalize to `command`/`cwd` in emitted tool input while
  keeping original raw data.

## Files likely touched

The bridge is a separate repository, `agy-acp`; paths below are relative to
that repository, not to Kandev:

- `src/main.rs`
- `src/adapter.rs`
- `src/types.rs`
- `src/streaming.rs`
- `src/tests.rs`
- `README.md`

## Verification

```bash
cd <agy-acp checkout>
cargo build
cargo test
cargo test -- --include-ignored
```

Use a JSON-RPC smoke probe against the built bridge to assert its
`initialize`, `session/new`, mode update, and effort update response shapes.
Use the authenticated ignored E2E tier only when the local Antigravity account
is available; it is supplementary to deterministic unit coverage.

## Output contract

Report the exact ACP payload and child-command semantics implemented, test
output summaries, legacy-session result, and any upstream `agy` incompatibility
observed during the smoke probe.
