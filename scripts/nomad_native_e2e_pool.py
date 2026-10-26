"""Pool create-fixture scenario: placement onto a node pool, then production cleanup, on a real worker.

pool-placement creates a pool profile (a node pool and sizing, no node id and no SSH target), launches a task and
proves the job named no node, Nomad placed the single allocation on a node carrying the Kandev node contract, the
durable state is pool-placed on that node, and the agent is attached. It then runs the same archive-and-assert
production cleanup as cleanup-positive.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path
from typing import Any, Callable
from urllib.parse import urlsplit

import nomad_native_e2e_fixture as fx
import nomad_native_e2e_wo06 as wo06
import nomad_native_e2e_wo07 as wo07
from nomad_native_e2e import PreflightError

SCENARIOS = ("pool-placement",)
CONTRACT_KEYS = ("kandev.attach", "kandev.state_root")
NODE_ID_TARGET = "${node.unique.id}"


def contract_problems(meta: dict[str, Any]) -> list[str]:
    """Why a node's metadata is not a usable Kandev node contract; empty when it is."""
    problems = [f"missing {key}" for key in CONTRACT_KEYS if not meta.get(key)]
    attach = meta.get("kandev.attach")
    if attach not in (None, "", "local", "ssh"):
        problems.append(f"kandev.attach {attach!r} is not local or ssh")
    if attach == "ssh":
        problems += [f"missing {key}" for key in ("kandev.ssh_host", "kandev.ssh_host_key_sha256") if not meta.get(key)]
    if attach == "local" and not meta.get("kandev.host_identity_sha256"):
        problems.append("missing kandev.host_identity_sha256")
    return problems


def job_named_node(job: dict[str, Any]) -> bool:
    """True when the registered job pins itself to a node by id, at job, group or task level."""
    scopes = [job, *(job.get("TaskGroups") or [])]
    scopes += [task for group in job.get("TaskGroups") or [] for task in group.get("Tasks") or []]
    return any(c.get("LTarget") == NODE_ID_TARGET for scope in scopes for c in scope.get("Constraints") or [])


class PoolScenario(wo07.Wo07Scenario):
    def verify_node_identity(self) -> dict[str, Any]:
        """Pool paths come from the node's own kandev.state_root; they are recorded so cleanup-fixture uses the same ones."""
        identity = super().verify_node_identity()
        problems = contract_problems(self.node().get("Meta") or {})
        if problems:
            raise PreflightError(f"the fixture node does not carry the Kandev node contract: {', '.join(problems)}")
        self.r.save_state(pool_state_root=self.node()["Meta"]["kandev.state_root"])
        return {**identity, "pool_state_root": self.r.state["pool_state_root"]}

    def production_settled(self, env: dict[str, Any], pending: list[str]) -> dict[str, Any] | None:
        """Cleanup is settled only once the pool workspace and the per-environment fence directory are gone as well."""
        result = super().production_settled(env, pending)
        if result is None:
            return None
        workspace = self.read_state(env).get("workspace_path")
        state_root = self.r.state["pool_state_root"]
        if not workspace or not workspace.startswith(state_root + "/"):
            raise PreflightError("the recorded pool workspace is not under the node state root")
        remaining = []
        for label, path in (("workspace", workspace), ("fence directory", f"{state_root}/environments/{env['environment_id']}")):
            code, _ = self.r.ssh(f"[ -e '{path}' ] || [ -L '{path}' ]")
            if code == 0:
                remaining.append(label)
        if remaining:
            pending.append(f"pool {', '.join(remaining)} remains")
            return None
        return {**result, "workspace_removed": True, "fence_directory_removed": True}

    def assert_placement(self, env: dict[str, Any]) -> dict[str, Any]:
        pool = self.cfg["pool"]
        job = self.r.nomad(f"/v1/job/{env['job_id']}")
        job = job.get("Job", job)
        if job_named_node(job):
            raise PreflightError("the pool job names a node by id")
        if job.get("NodePool") != pool["node_pool"]:
            raise PreflightError(f"the pool job is in node pool {job.get('NodePool')!r}, expected {pool['node_pool']!r}")
        allocs = self.job_allocs(env["job_id"])
        if len(allocs) != 1:
            raise PreflightError(f"the pool job has {len(allocs)} allocations, expected exactly one")
        node = self.r.nomad(f"/v1/node/{allocs[0]['NodeID']}")
        if node.get("NodePool") != pool["node_pool"]:
            raise PreflightError("the allocation landed on a node outside the configured pool")
        problems = contract_problems(node.get("Meta") or {})
        if problems:
            raise PreflightError(f"the placed node does not carry the Kandev node contract: {', '.join(problems)}")
        state = self.read_state(env)
        if state.get("placement_mode") != "pool" or state.get("node_id") != allocs[0]["NodeID"]:
            raise PreflightError("durable state is not pool-placed on the allocation's node")
        if not state.get("node_contract_sha256"):
            raise PreflightError("durable state has no node contract digest")
        return {"node_id": state["node_id"], "node_pool": node["NodePool"], "attach": node["Meta"]["kandev.attach"],
                "job_names_node": False, "allocations": 1, "node_contract_sha256": state["node_contract_sha256"],
                "placement": state.get("placement")}

    def common_prelude(self, step: Callable[..., Any], runner: fx.Runner, cfg: dict[str, Any]) -> tuple[dict[str, str], dict[str, Any], dict[str, Any]]:
        result = super().common_prelude(step, runner, cfg)
        self.data["placement"] = step("assert-pool-placement", lambda: self.assert_placement(self.envs["A"]))
        return result


def drive_pool_placement(runner: fx.Runner, cfg: dict[str, Any], ops: fx.Ops, root: Path) -> dict[str, Any]:
    guard = wo07.OperatorGuard(ops)
    scn = PoolScenario(runner, cfg, "pool-placement", guard)
    try:
        report = wo07._positive_steps(runner, cfg, runner.step, scn, guard)
    finally:
        guard.restore()
    return {**report, "pool": True, "placement": scn.data.get("placement")}


def check_scenario_config(cfg: dict[str, Any], scenario: str) -> None:
    if scenario not in SCENARIOS:
        raise PreflightError("unknown pool scenario")
    if not cfg.get("pool"):
        raise PreflightError("the pool scenarios require a pool block in the bootstrap config")
    parsed = urlsplit(cfg["nomad"]["address"])
    if parsed.scheme != "http" or parsed.hostname not in ("127.0.0.1", "localhost", "::1"):
        raise PreflightError("the pool scenarios mutate Nomad: nomad.address must be a loopback http address")


def run_scenario(config: Any, scenario: str, ops: fx.Ops | None = None, **kwargs: Any) -> int:
    ops = ops or fx.Ops()

    def check(cfg: dict[str, Any]) -> None:
        check_scenario_config(cfg, scenario)
        wo06.check_no_quarantine_marker(cfg, ops)

    return fx.run_create_fixture(
        config, ops=ops, drive=drive_pool_placement, mode=f"created-real-{scenario}", label=scenario,
        stop_owned_on_failure=True, check_config=check, state_extra=wo06._state_extra(scenario), **kwargs,
    )


def create_fixture_command(config_path: str, scenario: str) -> int:
    try:
        config = json.loads(Path(config_path).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        print(f"real {scenario} bootstrap failed: config is unreadable", file=sys.stderr)
        return 2
    return run_scenario(config, scenario)
