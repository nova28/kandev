---
id: "07-cleanup-quarantine"
title: "Cooperative cancellation and quarantine"
status: pending
wave: 7
depends_on:
  - "06-admission-disconnect"
plan: "plan.md"
requirements:
  - REQ-EXECUTORS-NOMAD-002
  - REQ-EXECUTORS-NOMAD-003
  - REQ-EXECUTORS-NOMAD-004
acceptance_criteria:
  - AC-EXECUTORS-NOMAD-002.2
  - AC-EXECUTORS-NOMAD-003.1
  - AC-EXECUTORS-NOMAD-003.2
  - AC-EXECUTORS-NOMAD-003.3
  - AC-EXECUTORS-NOMAD-003.4
  - AC-EXECUTORS-NOMAD-004.3
system_design:
  - ../../specs/executors/system-design/nomad-native-executor.md
---

# Task 07: Cooperative cancellation and quarantine

## Summary

Complete native positive and negative cleanup with actual managed processes. Persist a fenced successful drain receipt before whole-job stop and retain all uncertain outcomes.

## In scope

- Current cleanup claim obtains exact ServerIdentity seal/drain, persists resource/generation/operation/claim receipt, removes only owned bootstrap secrets and stops whole job purge=false. Confirm all exact allocations/tasks terminal on original responsive node before exact worker-fence compare-owner release and inventory release.
- Test cancellation/archive/delete/reset and failed-launch rollback, duplicate cleanup, stale receipt/claim, child/grandchild plus shell/LSP/Git/provisional owners and rejected post-seal starts.
- Fail/timeout drain, lose reply before/after checkpoint, crash/change runtime, lose node/history or add unexpected allocation. Assert no false absence, retained cleanup_pending and actual node ineligibility/interlock before terminalization.
- Document explicit externally verified operator readmission; no auto reboot/kill-all/re-enable shortcut.

## Out of scope

- Rendered UI, Docker/VM execution, plugin implementation, provider SDK/protobuf changes, model-backed workloads, and production rollout.

## Owner-retention integration

`ResetTaskEnvironment` and `teardownEnvironmentResources` in
`internal/task/service/service_task_environments.go` must recognize retained
Nomad environments before deleting their owner rows. An idle or ordinarily
stopped session can still own a live allocation. Add the corresponding Nomad
inventory guard to SQLite `DeleteTaskEnvironment`; absence of a local active
session is insufficient cleanup evidence.

Task deletion and cascade cleanup currently capture durable cleanup intent but
can cascade-delete the session/environment rows required by
`validateNomadExecutorCheckpointOwner`. Keep the canonical cleanup authority
available until the receipt and exact remote absence are checkpointed. Cover
reset, delete and cascade with retained ready/stopped inventory, cleanup failure
and successful cleanup; an orphan executor row alone does not satisfy this
contract. Reuse the existing durable cleanup jobs and reset claims.

## Acceptance

- Cooperative cancellation verifies managed child/grandchild groups absent before durable receipt and exact whole-job stop, then releases only correct inventory; repeated cleanup is safe.
- Missing/mismatched/stale evidence and runtime/node/history uncertainty never confirm absence; failures retain identities, quarantine and retry information through backend restart.
- Sanitized evidence exposes no secrets/payload; operator recovery requires exact external process absence and dedicated ownership before explicit readmission.

## Verification

All new paths/test names are future deliverables. Run the complete block from
any shell with the named absolute root and fixture-only config set. Use TDD for
changed logic; no-tests-to-run or a skipped required real scenario fails the
work order. Do not mark done based only on existing tests.

```bash
: "${KANDEV_ROOT:?Set absolute Kandev worktree path}"
: "${KANDEV_NOMAD_TEST_CONFIG:?Set absolute fixture-only JSON config path}"
(cd "$KANDEV_ROOT/apps/backend" && go test -race ./internal/agentctl/server/api ./internal/agentctl/server/instance ./internal/agentctl/server/process ./internal/agent/runtime/lifecycle ./internal/agent/nomad -run 'TestRuntimeSealAndDrain|TestRuntimeDrainAdmission|TestNomadCleanupEvidence|TestNomadWorkerQuarantine|TestNomadDestroyIdentity' -count=1)
(cd "$KANDEV_ROOT" && scripts/nomad-native-e2e run --require-real --scenario cleanup-positive --config "$KANDEV_NOMAD_TEST_CONFIG")
(cd "$KANDEV_ROOT" && scripts/nomad-native-e2e run --require-real --scenario cleanup-negative --config "$KANDEV_NOMAD_TEST_CONFIG")
```

## Files likely touched

- `apps/backend/internal/agent/nomad/destroy.go and tests (new)`
- `apps/backend/internal/agent/runtime/lifecycle/executor_nomad_cleanup.go and tests (new)`
- `apps/backend/internal/agent/nomad/integration/cleanup_test.go and process evidence fixture (new)`
- `scripts/nomad-native-e2e; docs/public/executors.md operator recovery`
- `Generic drain files from task 01 only for demonstrated matrix defects`

## Dependencies

[Task 06](task-06-admission-disconnect.md).

## Risks

Success is scoped to trusted managed groups. Terminal allocation, missing PID, closed port or ownership/shutdown response does not substitute for successful drain; runtime crash/daemonized escape remains quarantined.

## Parallelism

`sequential`

## Inputs

- [Requirements](../../specs/executors/requirements/nomad-native-executor.md), using frontmatter IDs.
- [System design](../../specs/executors/system-design/nomad-native-executor.md), matching implementation boundary and current source.
- [Plan](plan.md), including strict real-worker runner/prerequisites.
- [ADR](../../decisions/2026-10-24-native-nomad-cleanup-evidence.md).
- Read applicable scoped `AGENTS.md` before editing code.

## Results

Pending. Implementation and verification evidence have not yet been recorded.
