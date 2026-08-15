---
id: "02-kandev-profile-controls"
title: "Expose explicit Antigravity profile switches"
status: ready
wave: 2
depends_on: ["01-bridge-session-controls"]
plan: "plan.md"
spec: "../../specs/agents/antigravity-acp.md"
parallelism: sequential
---

# Task 02: Expose Explicit Antigravity Profile Switches

## Intent

Add Kandev agent-catalogue metadata for the bridge's explicit trusted-local
controls, while relying on existing generic ACP profile UI for Start Mode and
Effort.

## Acceptance

- The Antigravity agent catalog advertises two curated profile CLI flags:
  `--dangerously-skip-permissions` and `--sandbox`, with clear risk-oriented
  labels and descriptions.
- Both switches seed and persist as profile CLI flags using the ordinary
  curated-flag path, not the deprecated dangerous-permission field.
- Agent discovery continues to require both `agy-acp` and `agy`, reports
  session-resume support, and does not claim MCP or remote support.
- The agent description accurately states current local-only behavior and
  removes the now-stale claim that permission bridging is unsupported.
- Existing generic ACP mode/config profiles need no dedicated React branch.

## Files likely touched

- `apps/backend/internal/agent/agents/antigravity_acp.go`
- `apps/backend/internal/agent/agents/antigravity_acp_test.go`
- `docs/specs/agents/antigravity-acp.md`
- `docs/plans/antigravity-acp-controls/plan.md`

## Verification

```bash
cd <kandev checkout>
rtk go test ./apps/backend/internal/agent/agents -run Antigravity -count=1
rtk make -C apps/backend test
```

If the local deployment is running, create or edit a local Antigravity profile
and confirm the existing settings page shows the bridge-provided Mode/Effort
and the two curated flags without a frontend code change.

## Output contract

Report the catalog keys, flags, default state, rendered labels/descriptions,
focused and full backend test results, and the profile UI observation.
