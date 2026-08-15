---
id: "03-contract-and-local-verification"
title: "Verify the end-to-end local integration"
status: ready
wave: 3
depends_on: ["01-bridge-session-controls", "02-kandev-profile-controls"]
plan: "plan.md"
spec: "../../specs/agents/antigravity-acp.md"
parallelism: sequential
---

# Task 03: Verify the End-to-End Local Integration

## Intent

Build and install the updated bridge, rebuild the local Kandev deployment, and
verify a real profile and task session without disturbing currently running
benchmark tasks.

## Acceptance

- The installed `agy-acp --help` exposes the explicit bridge switches.
- A Kandev profile can select Start Mode and Effort and persists those choices.
- A trusted local benchmark profile can enable skip permissions through the
  curated flag instead of `AGY_EXTRA_ARGS`.
- A small task emits a visible shell command and working directory in Kandev
  chat.
- An existing session/profile without the new values still starts and resumes.

## Verification

1. Run the deterministic bridge and Kandev test suites from Tasks 01 and 02.
2. Install the release bridge binary to the operator's configured user-local
   binary directory so `agy-acp` resolves on Kandev's process `PATH`.
3. Rebuild and restart only the local deployment at `http://localhost:8817`.
4. Use the Kandev MCP task/profile APIs for state changes; use the browser only
   to verify visual settings and task-chat rendering.
5. Record the commands and observed results in this task's `Results` section
   before marking it done.

## Risks

- Do not terminate an active benchmark task merely to refresh a profile.
- Permission bypass testing is limited to the dedicated trusted local worktree
  profile. Never enable it by default for ordinary profiles.
- A failure to obtain upstream token/cost data is not a failure of this scope;
  it remains explicitly deferred by the spec.
