# Nomad WO05: pre-ready crash recovery, real proof

Work order 05 (`docs/plans/nomad-native-executor/task-05-recovery.md`), fenced
environment readiness publication. Evidence file:
[`pre-ready-recovery-run9.json`](pre-ready-recovery-run9.json), the runner's
`evidence.json` with absolute fixture paths replaced by `<fixture-home>`.

## Result: passed (run 9, exit 0)

- Command: `scripts/nomad-native-e2e run --require-real --scenario pre-ready-recovery --create-fixture --config <bootstrap outside the repository>`.
- Runner at `54ab535df2`; backend and agentctl built from `0e73ef6a15` (kandev sha256 `87b63baed495b1ffb369c1c5128262ef451dac85dd725e0bda42c1bfa4f6d44a`, agentctl-darwin-arm64 sha256 `328d8409f9569592776eb93240e58b673ef48a0495268f6198a8b98560ed5fde`, codesign verified). All 11 steps passed.
- Cluster: throwaway local Nomad 2.0.7 on loopback, darwin/arm64 client, fixture sshd with a pinned host key. No real cluster or live service was touched.
- The fixture backend was SIGKILLed at the fixture-only pre-publish barrier, after the workspace input was durable and the inventory phase was `ready` while the `task_environments` row was still `creating`. It was restarted without the barrier.
- Before any prompt (`prompts_sent: 0`): the environment became `ready`, a normal workspace read returned the expected file, the recovered checkout HEAD `4c687ac69a70a67447ba936ed7b79ac861002683` (read over the pinned read-only SSH channel, since git snapshots exist only after an agent turn) equalled the fixture git-daemon repository HEAD, and the execution, job, allocation, node and fence were unchanged.

| Identity | Before crash | After recovery |
| --- | --- | --- |
| execution | `a57f4a03-7025-4470-af8f-ad2c8703fe72` | same |
| job | `kandev-50e5c3075a93089e39613eafbfd3ea99`, create/modify index 153/153 | same |
| allocation | `97fa6e7b-fb6d-ae9e-cbf1-a13ae25b33cf` | same |
| node | `5dd33836-f337-39c7-8bd3-d60004c67585` | same |
| fence owner sha256 | `80abea59...7d17` | same |
| environment status | `creating` | `ready` |
| remote process count | 2 | 2 |

Backend pids: 89103 before the crash, 89811 after the restart.

## Cleanup is an operator teardown, not production cleanup

An ordinary `session.stop` retains the allocation, fence, controller and
workspace (the work order 04 contract). The run asserted that retention
(allocation and job `running`, fence present and unchanged, 2 remote processes)
and recorded `production_cleanup: false`. Production whole-job stop remains work
order 07's. After the run the operator purged the retained job through the local
Nomad API and removed the fence directory; the cluster ended with 0 jobs and 0
allocations.

## Earlier attempts

Runs 1 to 6 failed and exposed runner bugs, fixed before run 7. Run 7 passed but never proved the repository HEAD. Run 8 passed every recovery step and failed only the new HEAD assertion, which wrongly read git snapshots (empty before any turn); run 9 reads HEAD over SSH. Bugs fixed: no agent
profile on a fresh fixture home (now an inert ACP agent is created), a missing
settings interlock header, an incomplete Nomad executor config, a local
repository without a provider origin (now a runner-owned `git daemon` plus
`insteadOf` redirect), the fence being a directory (read `<fence>/owner`), and a
cleanup that wrongly expected the allocation to end after `session.stop`. Run 6
already passed every recovery step and failed only that cleanup expectation.

## Not covered

Run 9 supersedes run 7 and is the authoritative WO05 evidence. Production whole-job cleanup is work order 07.

The PostgreSQL race test for the publish CAS was not run (no PostgreSQL here).

Run 9 predates the loopback bind, daemon group-kill guard and SSH identity removal hardening in the runner. Those changes affect only the runner's own safety and cleanup, not the recovery path, and run 9 has not been re-run with them.
