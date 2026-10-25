---
id: "05-recovery"
title: "Ambiguous-operation and restart recovery"
status: in_progress
wave: 5
depends_on:
  - "04-real-session"
plan: "plan.md"
requirements:
  - REQ-EXECUTORS-NOMAD-002
  - REQ-EXECUTORS-NOMAD-004
acceptance_criteria:
  - AC-EXECUTORS-NOMAD-002.1
  - AC-EXECUTORS-NOMAD-002.2
  - AC-EXECUTORS-NOMAD-004.2
system_design:
  - ../../specs/executors/system-design/nomad-native-executor.md
---

# Task 05: Ambiguous-operation and restart recovery

## Summary

Prove original-operation recovery across lost register responses and backend restart. Retain one exact allocation, workspace and authenticated runtime.

## In scope

- Inject real post-commit response loss before host job/descriptor checkpoint; exercise found, unknown, conflict and positively never-submitted absence.
- Restart backend at allocating/staging/bootstrap/ready barriers with existing database/secret references; reattach exact allocation/NodeID/ServerIdentity without prompt duplication.
- Cover changed profile/input digest, stale generation/claim, foreign same-name job, unexpected allocation, history GC and retained CAS operation fences.

## Pre-instance continuation clarification

- Complete the original registered/allocated operation through a manager-owned
  pre-instance continuation before UUID allocation, using canonical authorized
  WorkspaceInfo and prepareExecutionCreateRequest with the original execution
  ID. Validate exact row/environment generation, operation/job/allocation/fence,
  admitted settings and current admission. GET/create-only-if-missing the
  original instance, then normal registration and first ACP initialization.
  Do not send/replay prompts or claim initialized-session recovery.
- Persist nonce value/reference before worker staging/register and token
  value/reference immediately after handshake, through existing manager vault
  and typed metadata CAS. Whitelist credential-reference updates; preserve
  unrelated metadata. Return AuthToken/BootstrapNonce on ExecutorInstance.
  Missing consumed credentials stay unknown; do not generate replacements.
- Test registered-without-instance continuation, ambiguous instance-create
  response, changed owner/config rejection and nonce/token checkpoint failure.
  A manual fixture-only original-credential repair is allowed only under the
  paired design's exact private-file/ownership/CAS procedure. Record it as
  operator repair, never automatic recovery proof. If credentials are lost,
  preserve/quarantine the operation and positively clear it before fresh work.

## Full job identity clarification

Follow the [startup continuation contract](../../specs/executors/system-design/nomad-native-startup-continuation.md)
for Created-session intent retention, post-readiness guarded workspace-only
continuation, ready-before-ACP classification, separately owned first dispatch
and cleanup-only operator repair of already-pruned test inventory. Retention
alone is insufficient: generic initialized recovery cannot consume pending
creation. Add the specified startup/terminal-race cases and a clean real backend
kill/restart without repair; repaired fixture cleanup is separate evidence.
Its workspace-input section also separates the exact original digest input
(including captured empty) from the resolved runtime path. Capture it in typed
intent metadata before dispatch and use it consistently in manager/executor
digest checks. Legacy continuation needs unique durable original provenance
and an exact saved-digest match; never rewrite a digest/job to fit projection.

Follow the [job identity contract](../../specs/executors/system-design/nomad-native-job-identity.md).
Persist original nonsecret staged paths, policy version and expected canonical
job-spec SHA before registration. Validate full raw job/allocation policy with
closed path-specific normalization before projecting to a small struct; owner
metadata alone is insufficient. Preserve true CreateIndex and JobModifyIndex
separately from volatile ModifyIndex. Pre-release missing evidence stays unknown.
Include default-round-trip and foreign behavioral-field tests named there.

## Fenced environment readiness publication

Guarded Created-session recovery reaches runtime phase Ready but never writes
`task_environments`: the orchestrator `persistTaskEnvironment` →
`FinalizeTaskEnvironmentMaterialization` path died with the crashed launch. The
environment stays `creating` with an empty path, so every ordinary workspace
read rejects once the recovery guard is released.

- Add a repository method `PublishRecoveredNomadEnvironmentReady` in a new
  `sqlite/task_environment_nomad_recovery.go`. In one transaction, using the
  Finalize lock order (materialization lock, cleanup barrier, recovery claim,
  inventory row, session and environment rows), require: a Created session bound
  to the environment; a `nomad` environment at the same ownership generation,
  `creating`, materialized by that session, with an empty or identical path; and
  an inventory row for the same execution whose decoded state is phase `ready` at
  the caller's revision, with matching operation, environment, generation and
  path plus non-empty allocation and server identity.
- Write the repo inventory through a helper extracted from Finalize, then CAS
  `status='ready', materialization_session_id='', workspace_path=?` guarded by
  the same fields and by the inventory row's `updated_at` token. Zero rows →
  `ErrExecutionRotated` and rollback. An already-Ready environment with the same
  identity returns `(false, nil)` without writing; a different path or
  generation is an error.
- In `continueCreatedNomadRecovery`, after the final snapshot validation and
  before `completeCreatedNomadRecovery`: re-read the inventory and require phase
  `ready`, publish under the exact in-flight guarded owner predicate (extracted
  from `ensureWorkspaceSessionAdmittedForCreatedNomadRecovery`), then re-read and
  require environment `ready`. A failure keeps the guard and pending entry; the
  retry lands in the idempotent branch.
- Companion risk: `reelectTaskEnvironmentMaterializationSQL` can re-elect an
  abandoned-looking Created Nomad environment while its allocation is live.
  Exclude owners with a non-absent Nomad inventory row, with a regression test.

Tests first: SQLite publication (exact success, stale generation, wrong
operation, stale revision, pre-ready phase, foreign materializer, cleanup or
recovery claim, non-Created session, idempotent replay, different path,
repo-backed task without inventory, one-winner concurrency) and lifecycle
(publish before guard release and ordinary admission succeeds afterwards, no
publish for a non-recovery or stopped caller, failure retains the guard, replay
after committed publish does not double-publish, pre-ready phase does not
publish). The real proof remains a fresh captured-input pre-ready crash whose
runner asserts environment `ready` and a normal workspace read before any prompt.

## Out of scope

- Rendered UI, Docker/VM execution, plugin implementation, provider SDK/protobuf changes, model-backed workloads, and production rollout.

## Acceptance

- A lost response or backend restart never produces a second environment or substitutes a workspace; recovery outcomes require exact original-operation evidence.
- Changed identity/generation/input or unknown history cannot mutate/delete the original or successor and retains retryable ownership.
- Ready restart restores authenticated control/instance/reverse API transport and one prompt/result sequence without re-running bootstrap or launching another controller.

## Verification

Run the focused behavioral checks and the strict attached-worker recovery
scenario. The Go cases below cover stateful protocol barriers; they do not
substitute for the real backend restart scenarios in the attached fixture.
No-tests-to-run or a skipped required real scenario fails the work order.

```bash
: "${KANDEV_ROOT:?Set absolute Kandev worktree path}"
: "${KANDEV_NOMAD_TEST_CONFIG:?Set absolute fixture-only JSON config path}"
(cd "$KANDEV_ROOT/apps/backend" && GOFLAGS=-p=1 go test ./internal/agent/nomad ./internal/agent/runtime/lifecycle -run '^(TestNomad(207ObservedResponseDefaultsAreExplicitAndStopRemainsBehavior|Create(ReconcilesCommittedRegistrationAfterResponseLoss|FailureRetainsDurableIntentWithoutClaimingAllocation|InstanceReattachesRetainedOperationWithoutRegisterOrRestage|IntentRetryReconcilesCommittedJobWithoutRegisteringAgain)|StagingBarrierRemainsUnknownAndDoesNotRegisterOnRetry|RegisteredAllocationBarrierResumesSameJob|BootstrapBarrierRetainsCleanupPendingAndRejectsRetry|RetainedOperationFailsClosedOnChangedInputOrMissingHistory|RetainedOperationRejectsUnexpectedAllocationSet|ReadyContinuationAttachesExistingInstanceWithoutCreatingReplacement|ReadyContinuationRejectsMissingInstanceWithoutCreatingReplacement|ReadyContinuationRequiresAttachOnlyFlag)|CreatedNomadRecoveryPublishesWorkspaceOnlyExecutionForOriginalOperation|RetainedNomadExecutionIdentityRequiresExactCurrentOwnerAndRecoverablePhase|UnpinnedNomadRecoveryOwnerBlocksWorkspaceUntilSnapshotIsAvailable)$' -count=1)
(cd "$KANDEV_ROOT" && scripts/nomad-native-e2e run --require-real --scenario recovery --config "$KANDEV_NOMAD_TEST_CONFIG")
```

## Files likely touched

- `apps/backend/internal/agent/nomad/recovery.go and tests (new)`
- `apps/backend/internal/agent/runtime/lifecycle/executor_nomad_recovery.go and tests (new)`
- `apps/backend/internal/agent/nomad/integration/recovery_test.go and fault barriers (new)`
- `scripts/nomad-native-e2e`

## Dependencies

[Task 04](task-04-real-session.md).

## Risks

Timeout does not cancel a committed register. A single missing job/empty allocation query after possible dispatch is unknown, never absence. Do not copy plugin-specific recovery wiring blindly.

## Parallelism

`sequential`

## Inputs

- [Requirements](../../specs/executors/requirements/nomad-native-executor.md), using frontmatter IDs.
- [System design](../../specs/executors/system-design/nomad-native-executor.md), matching implementation boundary and current source.
- [Plan](plan.md), including strict real-worker runner/prerequisites.
- [ADR](../../decisions/2026-10-24-native-nomad-cleanup-evidence.md).
- Read applicable scoped `AGENTS.md` before editing code.

## Results

The strict attached-fixture recovery runner passed for one committed registration
response loss and a backend restart from a ready allocation. It verified the
same task/session, execution, Nomad job and allocation, NodeID, server identity,
workspace, and process tree after restart. Two prior completed prompt/result
pairs remained single-delivery; the new post-restart inert prompt produced one
result. This is live evidence for the commit-response-loss and ready-restart
barriers only; it does not prove restart behavior at earlier startup phases.

Added deterministic lifecycle fault tests exercise five other outcomes:

- Staging interruption leaves typed `intent`; a retry with missing job history
  fails closed without another registration or instance creation. Although the
  test observed no registration call, a restarted production process cannot
  infer that fact from a missing GET, so it must retain the operation as
  unknown.
- A registered job with no allocation times out without changing job identity;
  when the exact allocation appears, retry reattaches and registers only once.
- Bootstrap rejection leaves the allocation in `cleanup_pending`; retries are
  rejected rather than generating a replacement credential or job.
- Changed worker/workspace/generation identity, missing job history, and
  multiple allocations all fail closed without dispatch or replacement.

These deterministic fixture tests are not live process-crash simulations. A
later diagnostic restart of the same retained pre-ACP operation reached
`ready` while the session remained `CREATED`, preserving the original
execution, job, allocation, NodeID, server identity, and workspace; the harness
submitted no prompt. That fixture had required an explicit pre-release
workspace-input repair, so this is operator-assisted continuation evidence,
not proof of automatic recovery for newly captured intents. The live restart
exposed a missing `nomad_environment_generation` projection in the prepared
request. The manager now derives that launch-only field from the canonical
validated workspace owner, rejects conflicting metadata, and keeps typed
checkpoint state authoritative. Lifecycle and SQLite DAO regressions cover the
corrected path. Live pre-ready crash recovery for a fresh, automatically
captured intent and the remaining identity/history negatives are still
required; until then the work order remains incomplete. A crash before durable
dispatch evidence can leave `intent` with no observable job; that state remains
unknown and is not treated as positively never submitted.

The Created-session recovery path now has a manager-owned workspace-only
continuation for exact nonterminal Nomad owners. It revalidates the current
owner, environment generation, immutable input digest, staged job identity and
credential references; it uses the original execution ID, publishes no ACP
session, and keeps workspace access guarded until publication and a final owner
read succeed. If publication succeeds but that final read is transiently
unavailable, a retry accepts only the exact already-tracked uninitialized
execution and does not call the runtime a second time. Ready-before-ACP
continuation is attach-only: the saved auth-token reference is required, the
runtime requires the exact existing agentctl instance, and a missing instance
fails without registration or replacement. Focused tests cover these rules
and an unpinned startup owner blocking workspace access. These are local
deterministic tests; the damaged live fixture has not yet been automatically
recovered by this new continuation path.
