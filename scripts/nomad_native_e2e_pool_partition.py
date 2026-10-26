"""Pool partition scenario: one node of a two-node pool is cut off from the Nomad server, the other is untouched.

pool-partition launches two environments (one per node), partitions the ssh node's Nomad client with the runner's TCP
relay and proves, while partitioned and after the heal, that the local environment is unchanged and still answers a
prompt, while the ssh environment keeps its single original allocation with no replacement and no new job. It then
archives both through production cleanup. The fixture's ssh-node Nomad client must use the relay's listen address
(partition.listen) as its servers address, so the relay sits between that client and the server RPC.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path
from typing import Any

import nomad_native_e2e_fixture as fx
import nomad_native_e2e_pool as pool
import nomad_native_e2e_wo06 as wo06
import nomad_native_e2e_wo07 as wo07
from nomad_native_e2e import PreflightError

SCENARIO = "pool-partition"
PROBE_PROMPT = "For this inert partition check, output exactly WO05_PARTITION_LOCAL_PROBE"
ISOLATION_FAILURE = "the ssh node never left ready during the partition: the client was not isolated (RPC bypass)"


def check_scenario_config(cfg: dict[str, Any]) -> None:
    pool.check_scenario_config(cfg, "pool-two-node")
    partition = cfg.get("partition")
    if not isinstance(partition, dict) or not partition.get("listen") or not partition.get("upstream"):
        raise PreflightError("pool-partition requires a partition block (listen, upstream) in the bootstrap config; "
                             "the ssh node's Nomad client must use partition.listen as its servers address")


class PartitionScenario(pool.TwoNodeScenario):
    def node_info(self, node_id: str) -> tuple[Any, Any]:
        node = self.r.nomad(f"/v1/node/{node_id}")
        return node.get("Status"), node.get("SchedulingEligibility")

    def message_ids(self, env: dict[str, Any]) -> list[Any]:
        return [item.get("id") for item in self.r.api("GET", f"/task-sessions/{env['session_id']}/messages", None).get("messages", [])]

    def snapshot_env(self, env: dict[str, Any]) -> dict[str, Any]:
        self.focus(env)
        inventory = self.r.inventory(env["session_id"], env["task_id"])
        return {"inventory": inventory, "nomad": self.r.nomad_snapshot(inventory), "fence": self.r.fence(env["environment_id"]),
                "tree_ident": wo06._ident(self.tree(env)), "messages": self.message_ids(env)}

    def check_new_jobs(self, when: str) -> None:
        new_jobs = self.kandev_jobs() - self.known_jobs
        if new_jobs:
            raise PreflightError(f"{when}: unexpected new kandev job(s) {sorted(new_jobs)}")

    def check_allocation(self, env: dict[str, Any], when: str) -> str | None:
        allocs = self.job_allocs(env["job_id"])
        if len(allocs) != 1:
            raise PreflightError(f"{when}: job of environment {env['label']} has {len(allocs)} allocations, expected exactly one (no replacement)")
        if allocs[0]["ID"] != env["allocation_id"]:
            raise PreflightError(f"{when}: allocation of environment {env['label']} was replaced")
        status = self.alloc(env["allocation_id"]).get("ClientStatus")
        if status in fx.TERMINAL_ALLOCATION_STATES:
            raise PreflightError(f"{when}: allocation of environment {env['label']} became {status!r}; the original allocation must survive")
        self.check_new_jobs(when)
        return status

    def check_unchanged(self, env: dict[str, Any], snap: dict[str, Any], when: str, messages: list[Any]) -> None:
        """Everything observable about an environment equals its snapshot, except the message list which must equal `messages`."""
        self.focus(env)
        label = env["label"]
        if self.r.inventory(env["session_id"], env["task_id"]) != snap["inventory"]:
            raise PreflightError(f"{when}: durable inventory of environment {label} changed")
        if wo06._ident(self.tree(env)) != snap["tree_ident"]:
            raise PreflightError(f"{when}: remote process tree of environment {label} changed")
        if self.r.fence(env["environment_id"]) != snap["fence"]:
            raise PreflightError(f"{when}: worker fence of environment {label} changed")
        if self.r.nomad_snapshot(snap["inventory"]) != snap["nomad"]:
            raise PreflightError(f"{when}: Nomad job or allocation identity of environment {label} changed")
        if self.message_ids(env) != messages:
            raise PreflightError(f"{when}: session messages of environment {label} changed unexpectedly")

    def probe_local(self, env: dict[str, Any], task: dict[str, str], before: list[Any]) -> list[Any]:
        """The only prompt sent after the snapshot: an accepted ack and a new message prove the local environment still answers."""
        ack = self.ops.prompt({"ws_base": f"ws://127.0.0.1:{self.r.state['port']}", "task_id": task["id"], "session_id": task["session_id"], "content": PROBE_PROMPT})
        if ack.get("accepted") is not True:
            raise PreflightError("the local environment did not accept the prompt sent during the partition")
        after = self.poll(lambda: (lambda ids: ids if len(ids) > len(before) else None)(self.message_ids(env)),
                          "the local environment produced no new message for the prompt sent during the partition")
        if after[:len(before)] != before:
            raise PreflightError("the local session lost earlier messages")
        return after


def _partition_steps(runner: fx.Runner, cfg: dict[str, Any], ops: fx.Ops, scn: PartitionScenario, relay: Any, guard: wo07.OperatorGuard) -> dict[str, Any]:
    step = runner.step
    scn.data["node"] = step("verify-node-identity", scn.verify_node_identity)
    step("poll-node-ready", lambda: scn.poll(scn.node_ready, "Nomad node is not ready and eligible"))
    scn.data["nodes"] = step("verify-two-nodes", scn.verify_two_nodes)
    step("start-git-daemon", runner.start_git_daemon)
    step("start-backend", lambda: runner.start_backend(None))
    step("create-fixture-objects", lambda: runner.setup_api_objects(cfg))
    task_a = {"id": runner.state["task_id"], "session_id": runner.state["session_id"]}
    step("await-a-running", lambda: scn.await_placed("A", task_a))
    task_b = runner.create_task("Nomad pool fixture B")
    step("await-b-running", lambda: scn.await_placed("B", task_b))
    tasks = {"A": task_a, "B": task_b}
    if scn.envs["A"]["node_id"] == scn.envs["B"]["node_id"]:
        raise PreflightError("both environments landed on the same node; spread placement did not use the second node")
    by_attach = {env["attach"]: env for env in scn.envs.values()}
    if sorted(by_attach) != ["local", "ssh"]:
        raise PreflightError("the two environments must run on one local node and one ssh node")
    local, remote = by_attach["local"], by_attach["ssh"]
    local_task, remote_task = tasks[local["label"]], tasks[remote["label"]]
    scn.data["spread"] = {"local": local["label"], "ssh": remote["label"]}
    layouts = {label: step(f"assert-workspace-{label.lower()}", lambda e=scn.envs[label]: scn.workspace_layout(e)) for label in ("A", "B")}
    scn.data["layouts"] = layouts
    for label in ("A", "B"):
        step(f"start-managed-process-tree-{label.lower()}", lambda e=scn.envs[label], t=tasks[label]: scn.start_managed_tree(e, t, {}))
    scn.known_jobs = scn.kandev_jobs()
    snaps = {"local": step("snapshot-local", lambda: scn.snapshot_env(local)), "ssh": step("snapshot-ssh", lambda: scn.snapshot_env(remote))}
    if not snaps["ssh"]["tree_ident"] or not snaps["local"]["tree_ident"]:
        raise PreflightError("an environment has no remote process tree to compare across the partition")
    messages = {"local": snaps["local"]["messages"]}
    seen = scn.data["observed_statuses"] = []

    def observe(phase: str) -> str | None:
        status = scn.check_allocation(remote, f"{phase} partition")
        seen.append({"phase": phase, "node": scn.node_info(remote["node_id"])[0], "ssh_allocation": status})
        return status

    def partition() -> None:
        relay.partition()

        def isolated() -> bool:
            observe("during")
            if scn.node_info(local["node_id"])[0] != "ready":
                raise PreflightError("the local node left ready while only the ssh node was partitioned")
            return scn.node_info(remote["node_id"])[0] != "ready"

        scn.poll(isolated, ISOLATION_FAILURE)
        scn.check_allocation(local, "during partition")
        scn.check_unchanged(local, snaps["local"], "during partition", messages["local"])
        messages["local"] = scn.probe_local(local, local_task, messages["local"])
        scn.check_unchanged(local, snaps["local"], "after the probe prompt", messages["local"])
        scn.check_allocation(remote, "during partition")

    step("partition-and-assert-isolation", partition)

    def heal() -> dict[str, Any]:
        relay.heal()
        scn.poll(lambda: scn.node_info(remote["node_id"]) == ("ready", "eligible"), "the ssh node did not return to ready and eligible after the partition healed")
        status = scn.poll(lambda: (lambda s: s if s not in (None, "unknown") else None)(observe("healed")),
                          "allocation of the ssh environment stayed unknown after the partition healed")
        if status != "running":
            raise PreflightError(f"allocation of the ssh environment has ClientStatus {status!r} after the heal, expected running")
        scn.check_unchanged(remote, snaps["ssh"], "after heal", snaps["ssh"]["messages"])
        scn.check_allocation(local, "after heal")
        scn.check_unchanged(local, snaps["local"], "after heal", messages["local"])
        return {"ssh_allocation_client_status": status, "prompts_sent_to_ssh": 0, "prompts_sent_to_local": 1}

    scn.data["after_heal"] = step("heal-and-assert-original-allocations", heal)
    for label in ("A", "B"):
        step(f"archive-task-{label.lower()}", lambda t=tasks[label]: scn.archive(t))
    for label in ("A", "B"):
        def settled(e=scn.envs[label]) -> dict[str, Any]:
            scn.r.use_node(e["attach"], e["state_root"])
            pending: list[str] = []
            return scn.settle_note(pending, f"production cleanup of {e['label']} did not settle before timeout")(lambda: scn.production_settled(e, pending))

        scn.data["identities"][label]["cleanup"] = step(f"await-cleanup-{label.lower()}", settled)
        step(f"assert-cleaned-{label.lower()}", lambda e=scn.envs[label], l=layouts[label]: scn.assert_pool_cleaned(e, l))

    def assert_no_operator() -> None:
        guard.assert_clean()
        guard.armed = False

    step("assert-no-operator-cleanup", assert_no_operator)
    return step("teardown", lambda: scn.finish({"released": [], "operator_calls": []}))


def drive_pool_partition(runner: fx.Runner, cfg: dict[str, Any], ops: fx.Ops, root: Path) -> dict[str, Any]:
    check_scenario_config(cfg)
    guard = wo07.OperatorGuard(ops)
    scn = PartitionScenario(runner, cfg, SCENARIO, guard)
    relay = None
    try:
        relay = wo06._make_relay(ops, cfg)
        cleanup = _partition_steps(runner, cfg, ops, scn, relay, guard)
    except Exception as exc:
        # The relay is healed and keeps serving so cleanup-fixture can reach the client; it dies with this process.
        note = wo06.RELAY_INSTRUCTION.format(upstream=cfg["partition"]["upstream"])
        if relay is not None:
            try:
                relay.heal()
            except Exception as heal_exc:
                note += f" (the relay could not be healed: {heal_exc})"
            scn.data["relay_partitioned_at_exit"] = bool(getattr(relay, "partitioned", False))
        scn.data["relay_note"] = note
        raise PreflightError(f"{exc}; {note}") from exc
    finally:
        guard.restore()
    scn.data["relay_partitioned_at_exit"] = bool(getattr(relay, "partitioned", False))
    relay.stop()
    return {"scenario": scn.name, "pool": True, "partition": True, "production_cleanup": True, "operator_cleanup_used": False,
            "release": "none", "cleanup": cleanup, "observed_statuses": scn.data["observed_statuses"], "environments": wo07._env_report(scn)}


def run_scenario(config: Any, ops: fx.Ops | None = None, **kwargs: Any) -> int:
    ops = ops or fx.Ops()

    def check(cfg: dict[str, Any]) -> None:
        check_scenario_config(cfg)
        wo06.check_no_quarantine_marker(cfg, ops)

    return fx.run_create_fixture(
        config, ops=ops, drive=drive_pool_partition, mode=f"created-real-{SCENARIO}", label=SCENARIO,
        stop_owned_on_failure=True, check_config=check, state_extra=wo06._state_extra(SCENARIO), **kwargs,
    )


def create_fixture_command(config_path: str) -> int:
    try:
        config = json.loads(Path(config_path).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        print(f"real {SCENARIO} bootstrap failed: config is unreadable", file=sys.stderr)
        return 2
    return run_scenario(config)
