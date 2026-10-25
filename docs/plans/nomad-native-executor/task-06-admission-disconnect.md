---
id: "06-admission-disconnect"
title: "Capacity and actual client disconnect"
status: pending
wave: 6
depends_on:
  - "05-recovery"
plan: "plan.md"
requirements:
  - REQ-EXECUTORS-NOMAD-001
  - REQ-EXECUTORS-NOMAD-002
  - REQ-EXECUTORS-NOMAD-003
acceptance_criteria:
  - AC-EXECUTORS-NOMAD-001.2
  - AC-EXECUTORS-NOMAD-002.3
  - AC-EXECUTORS-NOMAD-003.3
  - AC-EXECUTORS-NOMAD-003.4
system_design:
  - ../../specs/executors/system-design/nomad-native-executor.md
---

# Task 06: Capacity and actual client disconnect

## Summary

Prove scheduler capacity and real disconnected-client behavior. Quarantine must prevent placement across operations and survive GCed allocation history.

## In scope

- Two distinct environments reserve measured CPU/memory; A consumes over half capacity including overhead, B genuinely remains unallocated. Release A only with valid evidence and observe B placement.
- Partition real Nomad client/server traffic while original ACP remains alive; observe no restart/reschedule/replacement before or after reconnect. Reconcile original resource/runtime only.
- Queue B before crashing A's leader with A managed child/grandchild still alive and backend down; B may allocate but its bootstrap must reject before any controller/workload launch. No timeout/PID/history can steal A's durable fence.
- Dedicated-owner checked Nomad node eligibility plus CAS quarantine marker blocks new work before cleanup release; unavailable control API preserves a durable pending interlock. Do not re-enable over newer operator intent.

## Out of scope

- Rendered UI, Docker/VM execution, plugin implementation, provider SDK/protobuf changes, model-backed workloads, and production rollout.

## Acceptance

- Real A/B evaluations demonstrate resource reservation rather than count=1; B starts only after safe A release and native reservation limits are described honestly.
- Actual Nomad client partition leaves one original workload running and no duplicate execution/allocation; reconnect restores original identities without another prompt.
- Quarantine makes the owned dedicated node ineligible, rejects new operations and survives history loss/restart; unresolved control-plane loss cannot reopen admission.

## Verification

All new paths/test names are future deliverables. Run the complete block from
any shell with the named absolute root and fixture-only config set. Use TDD for
changed logic; no-tests-to-run or a skipped required real scenario fails the
work order. Do not mark done based only on existing tests.

```bash
: "${KANDEV_ROOT:?Set absolute Kandev worktree path}"
: "${KANDEV_NOMAD_TEST_CONFIG:?Set absolute fixture-only JSON config path}"
(cd "$KANDEV_ROOT/apps/backend" && go test -race ./internal/agent/nomad ./internal/agent/runtime/lifecycle -run 'TestNomadNativeJob|TestNomadWorkerQuarantine|TestNomadAdmissionFence|TestNomadCrashWithQueuedEnvironment' -count=1)
(cd "$KANDEV_ROOT" && scripts/nomad-native-e2e run --require-real --scenario admission --config "$KANDEV_NOMAD_TEST_CONFIG")
(cd "$KANDEV_ROOT" && scripts/nomad-native-e2e run --require-real --scenario disconnect --config "$KANDEV_NOMAD_TEST_CONFIG")
```

## Files likely touched

- `apps/backend/internal/agent/nomad/job.go, quarantine.go and tests (new)`
- `apps/backend/internal/agent/runtime/lifecycle/executor_nomad_admission.go and tests (new)`
- `apps/backend/internal/agent/nomad/integration/admission_disconnect_test.go and partition fixture (new)`
- `scripts/nomad-native-e2e`

## Dependencies

[Task 05](task-05-recovery.md).

## Risks

Nomad policy must be tested on 2.0.7; defaults are not guarantees. Closing a Kandev stream is not this partition. Node eligibility is worker-wide, so shared nodes are unsupported.

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
