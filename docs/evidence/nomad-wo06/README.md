# Nomad WO06: capacity admission and client disconnect, real proof

Work order 06 (`docs/plans/nomad-native-executor/task-06-admission-disconnect.md`).
All runs use a throwaway local Nomad 2.0.7 (darwin/arm64 client) with a fixture-owned
headless Kandev; no real cluster or live service was touched. Absolute fixture paths are
replaced by `<fixture-home>`.

## Admission: passed (run 3, exit 0)

Evidence: [`admission-run3.json`](admission-run3.json). Backend built from `1972111756`
(kandev sha256 `5310659560a4f3800eab9a5c2b0316d6e8c81caa08d47b05ed593d8e1ef73dab`); runner at
`1d28f1bd56` plus the fixture fixes recorded in the work order Results.

- AC-EXECUTORS-NOMAD-001.2: the node has 48 MHz CPU, A requests 48 MHz and 1024 MB, so A
  uses all schedulable CPU. B's job registered with no allocation and a blocked evaluation
  naming `cpu`. A's inventory, Nomad snapshot, process tree and fence were identical before
  and after B was queued.
- The runner marked the node ineligible, ended A (purge disabled), verified terminal
  allocation, exact process absence and compare-owner fence removal, then restored
  eligibility. B had zero allocations through that window and exactly one running
  allocation, on the same node with a new allocation id and its own fence owner, afterwards.
- `production_cleanup` is false: release of A is the runner's operator teardown.

## Disconnect: pending

Runs 1 to 5 exposed runner defects (fixed) and one product finding: with the then-default `LostAfter=0`, Nomad 2.0.7 marked the
original allocation lost while its node was down. The job now carries a bounded,
profile-configurable `lost_after` (default 24h); the pass is recorded here once the rerun
succeeds.
