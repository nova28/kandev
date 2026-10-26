# Pool P3 evidence

## Runs at 3f7993e0c4 (two-node laptop fixture)

All three scenarios of `scripts/nomad_native_e2e.py` passed, rc 0, against a throwaway
Nomad server with two clients in node pool `kandev`: one `ssh` client and one `local`
client.

| Scenario | Run | Result |
|---|---|---|
| `pool-two-node` | `runs/nomad-e2e-fixture-m5kto0zn` | A on the ssh node, B on the local node; local worktree vs remote checkout; both cleaned by production cleanup |
| `pool-node-quarantine` | `runs/nomad-e2e-fixture-c4yng02i` | Drain failure quarantines one node only; B placed on the other; readmission |
| `pool-placement` (fresh profile) | `runs/nomad-e2e-fixture-e6nsjljz` | Job names no node; one allocation on a contract node; production cleanup |

Fixture boundary: both clients run on one Mac, where Nomad fingerprints 48 MHz, so both
set `cpu_total_compute=4000`; the local client reserves 1000 MHz / 2048 MB. This is not a
multi-machine fleet.

## Findings from those runs, and their fixes

1. Product leak (REQ-006, REQ-004.1): every pool environment left
   `<state_root>/pool/allocations/kandev-<job>/` (bootstrap script, `.sh.env` that may hold
   bootstrap secrets, ready marker, an agentctl copy) after proven cleanup.
   Fix: `AllocationDirRemoveCommand` runs in the cleanup tail after the workspace removal
   and before the fence release; a failure keeps the environment retained. Offline tests:
   `alloc_dir_pool_test.go`, `executor_nomad_pool_cleanup_test.go`. Live assertion:
   `production_settled` in `pool-placement` and `pool-two-node` now requires the allocation
   directory to be gone.
2. Runner: `pool-node-quarantine` teardown released B through operator teardown but left
   its worktree, branch `kandev/pool/<B>`, environment directory and allocation directory.
   Fix: the teardown now removes them and asserts the branch, the worktree registration and
   the directories are gone (`remove_b_leftovers`).

## Placement wording

AC-EXECUTORS-NOMADPOOL-001.3 matches Nomad: spread places each environment on the eligible
node with the lowest reserved-resource utilisation; binpack fills a node before using
another; placement is reservation-based, not live-load-based.

## Reruns at e2fc748583 (v0.98.0-59-ge2fc748583): all passed

| Scenario | Run | Result |
|---|---|---|
| `pool-placement` | `runs/nomad-e2e-fixture-10s2t0y7` | rc 0 |
| `pool-two-node` | `runs/nomad-e2e-fixture-mpm6g4oe` | rc 0 |
| `pool-node-quarantine` | `runs/nomad-e2e-fixture-5omgj8_b` | rc 0 |

After each run: no worker processes, no extra worktree, and no allocation directory or
local-state entry left, so the leak fix holds. One remainder: after `pool-node-quarantine`
the quarantined environment A's allocation directory stayed on the ssh node. Keeping it
while quarantined is correct; the readmission procedure now deletes it once absence is
verified (`readmit-remove-allocation-directory` step, public docs updated).

## Pending

The manual queue, partition and refusal procedures are being converted into runner scenarios
(`pool-queue`, `pool-partition`, `pool-refusals`); results are appended here.
