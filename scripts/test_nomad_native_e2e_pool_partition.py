import unittest
import unittest.mock
from pathlib import Path

import nomad_native_e2e_fixture as fx
import nomad_native_e2e_pool_partition as pp
from nomad_native_e2e import PreflightError

CFG = {"nomad": {"address": "http://127.0.0.1:4646"}, "pool": {"node_pool": "kandev"},
       "partition": {"listen": "127.0.0.1:45001", "upstream": "127.0.0.1:45002"}}
ENVS = {
    "A": {"label": "A", "attach": "local", "state_root": "/l", "node_id": "n-local", "job_id": "job-l", "allocation_id": "al-l",
          "environment_id": "env-l", "session_id": "s-l", "task_id": "t-l"},
    "B": {"label": "B", "attach": "ssh", "state_root": "/s", "node_id": "n-ssh", "job_id": "job-s", "allocation_id": "al-s",
          "environment_id": "env-s", "session_id": "s-s", "task_id": "t-s"},
}


class World:
    def __init__(self, **flags):
        self.flags, self.partitioned, self.log = flags, False, []
        self.node = {"n-local": "ready", "n-ssh": "ready"}
        self.allocs = {"job-l": [{"ID": "al-l"}], "job-s": [{"ID": "al-s"}]}
        self.status = {"al-l": "running", "al-s": "running"}
        self.jobs = {"job-l", "job-s"}
        self.messages = {"s-l": ["m1"], "s-s": ["m2"]}
        self.tree = {"env-l": [(1, "a")], "env-s": [(2, "b")]}
        self.fence = {"env-l": "f1", "env-s": "f2"}
        self.prompts = []

    def on_partition(self):
        self.partitioned = True
        if not self.flags.get("bypass"):
            self.node["n-ssh"] = "disconnected"
            self.status["al-s"] = "unknown"
        if self.flags.get("replaced"):
            self.allocs["job-s"].append({"ID": "al-new"})
        if self.flags.get("new_job"):
            self.jobs.add("kandev-new")
        if self.flags.get("ssh_lost"):
            self.status["al-s"] = "lost"
        if self.flags.get("local_tree"):
            self.tree["env-l"] = [(9, "z")]
        if self.flags.get("local_fence"):
            self.fence["env-l"] = "other"

    def on_heal(self):
        self.partitioned = False
        self.node["n-ssh"] = "ready"
        if not self.flags.get("stay_unknown"):
            self.status["al-s"] = "paused" if self.flags.get("not_running") else "running"
        if self.flags.get("ssh_tree_after"):
            self.tree["env-s"] = [(7, "q")]


class Relay:
    def __init__(self, world, fail_heal=False):
        self.world, self.calls, self.partitioned, self.fail_heal = world, [], False, fail_heal

    def start(self):
        self.calls.append("start")

    def partition(self):
        self.calls.append("partition")
        self.partitioned = True
        self.world.on_partition()

    def heal(self):
        self.calls.append("heal")
        if self.fail_heal:
            raise OSError("boom")
        self.partitioned = False
        self.world.on_heal()

    def stop(self):
        self.calls.append("stop")


def make_runner(world, ops):
    runner = type("R", (), {})()
    runner.ops, runner.state, runner.evidence = ops, {"nomad": {"node_id": "n-local"}, "task_id": "t-l", "session_id": "s-l", "port": 1}, \
        type("E", (), {"data": {"steps": [], "identities": {}}, "save": lambda self: None})()
    runner.step = lambda name, fn: fn()
    runner.poll = lambda t, i, probe, failure: fx.Runner.poll(runner, t, i, probe, failure)
    runner.use_node = lambda a, r: None
    runner.start_git_daemon = runner.start_backend = lambda *a: None
    runner.setup_api_objects = lambda cfg: None
    runner.create_task = lambda title: {"id": "t-s", "session_id": "s-s"}
    envs = {"s-l": "env-l", "s-s": "env-s"}
    runner.inventory = lambda sid, tid: {"session": sid, "job_id": "job-l" if sid == "s-l" else "job-s"}
    runner.fence = lambda env_id=None: world.fence[env_id]
    runner.nomad_snapshot = lambda inv: {"inv": inv["session"], "jobs": sorted(world.jobs & {"job-l", "job-s"})}
    runner.nomad = lambda path: {"Status": world.node[path.rsplit("/", 1)[1]], "SchedulingEligibility": "eligible"}
    runner.api = lambda method, path, body: {"messages": [{"id": m} for m in world.messages[path.split("/")[2]]]}
    return runner


class FakeScn(pp.PartitionScenario):
    world = None

    def verify_node_identity(self):
        return {"ok": True}

    def node_ready(self):
        return True

    def verify_two_nodes(self):
        return {"nodes": 2}

    def await_placed(self, label, task):
        self.envs[label] = dict(ENVS[label])
        self.data["identities"][label] = {}
        return {}

    def workspace_layout(self, env):
        return {"kind": "worktree" if env["attach"] == "local" else "checkout"}

    def start_managed_tree(self, env, task, snapshot):
        return []

    def kandev_jobs(self):
        return set(self.world.jobs)

    def job_allocs(self, job_id):
        return list(self.world.allocs[job_id])

    def alloc(self, allocation_id):
        return {"ClientStatus": self.world.status[allocation_id]}

    def tree(self, env):
        return [{"pid": p, "started": s} for p, s in self.world.tree[env["environment_id"]]]

    def archive(self, task):
        self.world.log.append(("archive", task["id"]))
        if self.world.flags.get("operator"):
            self.ops.nomad_stop("a", "t", "n", "j")

    def production_settled(self, env, pending):
        return {"phase": "absent"}

    def assert_pool_cleaned(self, env, layout):
        self.world.log.append(("cleaned", env["label"], layout["kind"]))
        return {}

    def finish(self, report):
        return {**report, "finished": True}


class PartitionBase(unittest.TestCase):
    def run_drive(self, cfg=CFG, fail_heal=False, probe_accepted=True, probe_adds=True, **flags):
        world = World(**flags)
        ticks = iter(range(10000))
        ops = fx.Ops()
        ops.sleep = lambda s: None
        ops.clock = lambda: next(ticks)
        ops.scenario_timeout, ops.poll_interval = 30, 0
        ops.nomad_stop = lambda *a: None
        self.relay = Relay(world, fail_heal)
        ops.relay_factory = lambda listen, upstream: self.relay

        def prompt(payload):
            world.prompts.append(payload["session_id"])
            if probe_adds:
                world.messages[payload["session_id"]].append(f"m-new-{len(world.prompts)}")
            return {"accepted": probe_accepted}

        ops.prompt = prompt
        runner = make_runner(world, ops)
        FakeScn.world = world
        self.world, self.runner = world, runner
        with unittest.mock.patch.object(pp, "PartitionScenario", FakeScn):
            try:
                return pp.drive_pool_partition(runner, cfg, ops, Path("/x"))
            finally:
                self.ops = ops

    def fails(self, message, **kw):
        with self.assertRaises(PreflightError) as ctx:
            self.run_drive(**kw)
        self.assertIn(message, str(ctx.exception))
        return str(ctx.exception)


class ConfigTest(unittest.TestCase):
    def test_partition_block_is_required(self):
        pp.check_scenario_config(CFG)
        for cfg in ({**CFG, "partition": None}, {**CFG, "partition": {"listen": "x"}},
                    {k: v for k, v in CFG.items() if k != "partition"}, {k: v for k, v in CFG.items() if k != "pool"}):
            with self.assertRaises(PreflightError):
                pp.check_scenario_config(cfg)

    def test_drive_refuses_before_touching_anything_without_partition(self):
        t = PartitionBase()
        with self.assertRaises(PreflightError):
            t.run_drive(cfg={"nomad": CFG["nomad"], "pool": CFG["pool"]})

    def test_scenario_name(self):
        self.assertEqual(pp.SCENARIO, "pool-partition")


class DriveTest(PartitionBase):
    def test_happy_path(self):
        report = self.run_drive()
        self.assertTrue(report["production_cleanup"] and not report["operator_cleanup_used"])
        self.assertEqual(self.relay.calls, ["start", "partition", "heal", "stop"])
        self.assertEqual(self.world.prompts, ["s-l"])
        self.assertEqual([x for x in self.world.log if x[0] == "archive"], [("archive", "t-l"), ("archive", "t-s")])
        self.assertEqual(sorted(x[1] for x in self.world.log if x[0] == "cleaned"), ["A", "B"])
        self.assertTrue(report["cleanup"]["finished"])
        phases = {(o["phase"], o["node"]) for o in report["observed_statuses"]}
        self.assertIn(("during", "disconnected"), phases)
        self.assertEqual(self.runner.evidence.data["after_heal"]["ssh_allocation_client_status"], "running")

    def test_relay_bypass_when_the_node_never_leaves_ready(self):
        message = self.fails("the client was not isolated (RPC bypass)", bypass=True)
        self.assertEqual(self.relay.calls[-1], "heal")

    def test_local_env_drift_fails(self):
        self.fails("remote process tree of environment A changed", local_tree=True)
        self.fails("worker fence of environment A changed", local_fence=True)

    def test_probe_must_be_accepted_and_add_a_message(self):
        self.fails("did not accept the prompt", probe_accepted=False)
        self.fails("produced no new message", probe_adds=False)

    def test_ssh_allocation_replaced(self):
        self.fails("has 2 allocations", replaced=True)

    def test_ssh_allocation_terminal(self):
        self.fails("became 'lost'", ssh_lost=True)

    def test_new_job_registered(self):
        self.fails("unexpected new kandev job(s) ['kandev-new']", new_job=True)

    def test_allocation_unknown_after_heal(self):
        self.fails("stayed unknown after the partition healed", stay_unknown=True)

    def test_allocation_not_running_after_heal(self):
        self.fails("expected running", not_running=True)

    def test_ssh_tree_changed_after_heal(self):
        self.fails("remote process tree of environment B changed", ssh_tree_after=True)

    def test_relay_healed_not_stopped_with_note_on_failure(self):
        message = self.fails("dies with it", bypass=True)
        self.assertIn("127.0.0.1:45002", message)
        self.assertEqual(self.relay.calls, ["start", "partition", "heal"])
        self.assertFalse(self.relay.partitioned)
        self.assertFalse(self.runner.evidence.data["relay_partitioned_at_exit"])

    def test_unhealable_relay_is_reported(self):
        self.fails("could not be healed: boom", fail_heal=True, bypass=True)
        self.assertTrue(self.runner.evidence.data["relay_partitioned_at_exit"])

    def test_operator_cleanup_fails_the_run_and_restores_ops(self):
        original = fx.Ops.nomad_stop
        self.fails("operator cleanup was used", operator=True)
        self.assertNotEqual(getattr(self.ops.nomad_stop, "__name__", ""), "inner")
        self.assertIs(fx.Ops.nomad_stop, original)

    def test_same_node_placement_fails(self):
        world_envs = {"A": dict(ENVS["A"]), "B": {**ENVS["B"], "node_id": "n-local"}}
        with unittest.mock.patch.dict(ENVS, world_envs):
            self.fails("same node")

    def test_both_local_fails(self):
        with unittest.mock.patch.dict(ENVS, {"B": {**ENVS["B"], "attach": "local", "node_id": "n-other"}}):
            self.fails("one local node and one ssh node")


if __name__ == "__main__":
    unittest.main()
