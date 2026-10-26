# Nomad worker pool P2 live evidence

Scope: REQ-EXECUTORS-NOMADPOOL-003 (ssh-node checkout), -004 (per-environment
fence), -006 (scratch in the allocation directory, workspace removal on proven
cleanup). The local-node git worktree is not covered here; its live proof is in
the P3 two-node run.

## Run 1 (passed)

- Build: `a1c7afce42` (`v0.98.0-48-ga1c7afce42`), laptop pool fixture, node pool
  `kandev`, ssh-attached node. Not run on Tank.
- Scenario: `pool-placement`, rc=0, all 12 steps (pool placement, production
  cleanup, no operator cleanup, teardown).
- Evidence: `/private/tmp/ff-wo05-live.rO8x/runs/nomad-e2e-fixture-tqnsw3x9/evidence.json`
  (operator-reported; the file lives on the operator's laptop).
- After the run: 0 leftover processes, `<state_root>/environments/` empty,
  `worker-state/<node>/` empty.
- The runner treats cleanup as settled only when the pool workspace and the
  per-environment fence directory are both gone.
