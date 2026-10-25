# Nomad WO05: pre-ready crash recovery, real proof

Work order 05 (`docs/plans/nomad-native-executor/task-05-recovery.md`), fenced
environment readiness publication. Evidence file:
[`pre-ready-recovery-run7.json`](pre-ready-recovery-run7.json), the runner's
`evidence.json` with absolute fixture paths replaced by `<fixture-home>`.

## Result: passed (run 7, exit 0)

- Command: `scripts/nomad-native-e2e run --require-real --scenario pre-ready-recovery --create-fixture --config <bootstrap outside the repository>`.
- Runner at `ce68dc0e2f`; backend built from `560dfd2b57` (kandev sha256 `ff11e58903f4f96b3fb5e62aeed90c0e892ca943105409e8b3ed42df14ba2e5e`, agentctl-darwin-arm64 sha256 `698ed0d6ebcc8156850fb6d19b9cbcf4a114cea12e0f5822bda8c4ca87e3cce8`).
- Cluster: throwaway local Nomad 2.0.7 on loopback, darwin/arm64 client, fixture sshd with a pinned host key. No real cluster or live service was touched.
- The fixture backend was SIGKILLed at the fixture-only pre-publish barrier, after the workspace input was durable and the inventory phase was `ready` while the `task_environments` row was still `creating`. It was restarted without the barrier.
- Before any prompt (`prompts_sent: 0`): the environment became `ready`, a normal workspace read returned the expected file, and the execution, job, allocation, node and fence were unchanged.

| Identity | Before crash | After recovery |
| --- | --- | --- |
| execution | `db602d5f-0381-4593-844a-39221183b1d9` | same |
| job | `kandev-431754849dd35561737413eb104e5ba4`, create/modify index 89/89 | same |
| allocation | `61b08712-ac6a-8269-b586-d1595e5b2f5b` | same |
| node | `5dd33836-f337-39c7-8bd3-d60004c67585` | same |
| fence owner sha256 | `b7816ac1...f761` | same |
| environment status | `creating` | `ready` |
| remote process count | 2 | 2 |

Backend pids: 72995 before the crash, 73802 after the restart.

## Cleanup is an operator teardown, not production cleanup

An ordinary `session.stop` retains the allocation, fence, controller and
workspace (the work order 04 contract). The run asserted that retention
(allocation and job `running`, fence present and unchanged, 2 remote processes)
and recorded `production_cleanup: false`. Production whole-job stop remains work
order 07's. After the run the operator purged the retained job through the local
Nomad API and removed the fence directory; the cluster ended with 0 jobs and 0
allocations.

## Earlier attempts

Runs 1 to 6 failed and exposed runner bugs, all fixed before run 7: no agent
profile on a fresh fixture home (now an inert ACP agent is created), a missing
settings interlock header, an incomplete Nomad executor config, a local
repository without a provider origin (now a runner-owned `git daemon` plus
`insteadOf` redirect), the fence being a directory (read `<fence>/owner`), and a
cleanup that wrongly expected the allocation to end after `session.stop`. Run 6
already passed every recovery step and failed only that cleanup expectation.

## Not covered

The PostgreSQL race test for the publish CAS was not run (no PostgreSQL here).
