import unittest
import unittest.mock

import nomad_native_e2e_fixture as fixture
import nomad_native_e2e_pool as pool
from nomad_native_e2e import PreflightError
from nomad_profile_payload import ProfilePayloadError, build_profile_payload, validate_profile_source
from test_nomad_native_e2e_wo06 import Wo06Base

POOL = {"node_pool": "kandev", "placement": "binpack", "ssh_user": "worker", "memory_max_mb": 2048}
SOURCE = {
    "schema_version": 1, "executor_id": "00000000-0000-0000-0000-000000000002",
    "profile_id": "00000000-0000-0000-0000-000000000003", "profile_name": "pool fixture",
    "nomad_token_ref": "$kandev-secret:00000000-0000-0000-0000-000000000004",
    "config": {"address": "http://127.0.0.1:4646", "namespace": "default", "region": "global", "datacenter": "dc1",
               "platform": "darwin/arm64", "node_pool": "kandev", "placement": "spread", "cpu_mhz": "100"},
}


def with_config(**updates):
    return {**SOURCE, "config": {**SOURCE["config"], **updates}}


class PoolPayloadTest(unittest.TestCase):
    def test_pool_profile_payload_carries_pool_keys_and_token_reference(self):
        payload = build_profile_payload(validate_profile_source(SOURCE))
        self.assertEqual(payload["config"]["node_pool"], "kandev")
        self.assertEqual(payload["config"]["nomad_token"], SOURCE["nomad_token_ref"])
        self.assertNotIn("node_id", payload["config"])

    def test_pool_profile_rejects_pinned_keys_and_missing_required(self):
        for updates in ({"node_id": "x"}, {"ssh_target": "a@b"}, {"workspace_root": "/x"}, {"bogus": "1"}):
            with self.assertRaises(ProfilePayloadError, msg=str(updates)):
                validate_profile_source(with_config(**updates))
        config = dict(SOURCE["config"])
        del config["datacenter"]
        with self.assertRaises(ProfilePayloadError):
            validate_profile_source({**SOURCE, "config": config})


class PoolBootstrapTest(Wo06Base):
    def test_pool_is_optional_and_validated(self):
        self.assertIsNone(self.validate()["pool"])
        self.config["pool"] = dict(POOL)
        self.assertEqual(self.validate()["pool"], POOL)

    def test_host_identity_token_file_must_be_a_private_regular_file(self):
        token = self.parent / "identity-token"
        token.write_text("secret-token\n")
        token.chmod(0o600)
        self.config["pool"] = {**POOL, "host_identity_token_file": str(token)}
        self.assertEqual(self.validate()["pool"]["host_identity_token_file"], str(token))
        token.chmod(0o644)
        with self.assertRaises(PreflightError):
            self.validate()
        self.config["pool"] = {**POOL, "host_identity_token_file": "relative/token"}
        with self.assertRaises(PreflightError):
            self.validate()

    def test_pool_rejections(self):
        bad = [{}, {"placement": "spread"}, {"node_pool": "bad pool"}, {"node_pool": "kandev", "placement": "random"},
               {"node_pool": "kandev", "node_id": "x"}, {"node_pool": "kandev", "ssh_user": "a@b"},
               {"node_pool": "kandev", "memory_max_mb": 0}, "x"]
        for value in bad:
            self.config["pool"] = value
            with self.assertRaises(PreflightError, msg=str(value)):
                self.validate()

    def test_pool_profile_config_names_no_node_or_worker_paths(self):
        self.config["pool"] = dict(POOL)
        cfg = self.validate()
        config = fixture.Runner.pool_profile_config(cfg)
        self.assertEqual(config["node_pool"], "kandev")
        self.assertEqual(config["placement"], "binpack")
        self.assertEqual(config["memory_max_mb"], "2048")
        for key in ("node_id", "ssh_target", "ssh_port", "ssh_host_fingerprint", "workspace_root", "allocation_root"):
            self.assertNotIn(key, config)

    def test_scenario_requires_pool_block_and_loopback_nomad(self):
        cfg = self.validate()
        with self.assertRaises(PreflightError):
            pool.check_scenario_config(cfg, "pool-placement")
        self.config["pool"] = dict(POOL)
        cfg = self.validate()
        pool.check_scenario_config(cfg, "pool-placement")
        with self.assertRaises(PreflightError):
            pool.check_scenario_config(cfg, "other")


class NodeContractTest(unittest.TestCase):
    SSH = {"kandev.attach": "ssh", "kandev.state_root": "/srv/kandev", "kandev.ssh_host": "w.example.test",
           "kandev.ssh_host_key_sha256": "a" * 64}

    def test_contract_problems(self):
        self.assertEqual(pool.contract_problems(self.SSH), [])
        self.assertEqual(pool.contract_problems({"kandev.attach": "local", "kandev.state_root": "/s", "kandev.host_identity_sha256": "b" * 64}), [])
        self.assertTrue(pool.contract_problems({}))
        self.assertIn("missing kandev.ssh_host", pool.contract_problems({k: v for k, v in self.SSH.items() if k != "kandev.ssh_host"}))
        self.assertIn("missing kandev.host_identity_sha256", pool.contract_problems({"kandev.attach": "local", "kandev.state_root": "/s"}))
        self.assertTrue(pool.contract_problems({**self.SSH, "kandev.attach": "rsh"}))

    def test_job_named_node_checks_every_scope(self):
        pin = {"LTarget": "${node.unique.id}", "Operand": "=", "RTarget": "n"}
        other = {"LTarget": "${attr.kernel.name}", "Operand": "=", "RTarget": "darwin"}
        self.assertFalse(pool.job_named_node({"Constraints": [other], "TaskGroups": [{"Tasks": [{}]}]}))
        self.assertTrue(pool.job_named_node({"Constraints": [pin]}))
        self.assertTrue(pool.job_named_node({"TaskGroups": [{"Constraints": [pin]}]}))
        self.assertTrue(pool.job_named_node({"TaskGroups": [{"Tasks": [{"Constraints": [pin]}]}]}))


class PoolFencePathTest(unittest.TestCase):
    STATE = {"allocation_root": "/fx/alloc/a", "node_id": "n1"}

    def test_pool_fence_path_uses_node_state_root(self):
        self.assertEqual(fixture._fence_path({**self.STATE, "pool_state_root": "/fx/worker"}), "/fx/worker/environments/unrecorded/active")

    def test_pool_fence_is_per_environment(self):
        state = {**self.STATE, "pool_state_root": "/fx/worker"}
        self.assertEqual(fixture._fence_path(state, "env-1"), "/fx/worker/environments/env-1/active")
        state["fence_environment_id"] = "env-2"
        self.assertEqual(fixture._fence_path(state), "/fx/worker/environments/env-2/active")
        self.assertNotEqual(fixture._fence_path(state, "env-1"), fixture._fence_path(state))

    def test_legacy_fence_path_is_unchanged(self):
        self.assertEqual(fixture._fence_path(self.STATE), "/fx/worker-state/n1/active-environment")


class OwnedFenceTest(unittest.TestCase):
    def sha(self, owner):
        import nomad_native_e2e_wo06 as wo06
        scn = wo06.Scenario.__new__(wo06.Scenario)
        scn.r = type("R", (), {"fence_owner": lambda self: owner})()
        return scn.owned_fence_sha({"job_id": "kandev-job-1"})

    def test_only_a_fence_naming_the_job_is_released(self):
        import hashlib
        self.assertEqual(self.sha("env kandev-job-1\n"), hashlib.sha256(b"env kandev-job-1\n").hexdigest())
        self.assertIsNone(self.sha("env kandev-job-2\n"))
        self.assertIsNone(self.sha(None))


class TwoNodeLayoutTest(unittest.TestCase):
    def scenario(self, output, code=0, executed=None):
        scn = pool.TwoNodeScenario.__new__(pool.TwoNodeScenario)
        calls = executed if executed is not None else []
        scn.read_state = lambda env: {"workspace_path": "/n/workspaces/environments/e1/g1"}
        scn.r = type("R", (), {"use_node": lambda self, a, r: calls.append(("use", a, r)),
                               "ssh": lambda self, cmd: (calls.append(("ssh", cmd)) or (code, output))})()
        return scn

    def env(self, attach):
        return {"label": "A", "attach": attach, "state_root": "/n", "environment_id": "e1"}

    def test_local_node_must_hold_a_worktree_on_the_environment_branch(self):
        layout = self.scenario("worktree kandev/pool/e1 /src/.git").workspace_layout(self.env("local"))
        self.assertEqual(layout["kind"], "worktree")
        with self.assertRaises(PreflightError):
            self.scenario("checkout main /n/ws/.git").workspace_layout(self.env("local"))
        with self.assertRaises(PreflightError):
            self.scenario("worktree main /src/.git").workspace_layout(self.env("local"))

    def test_ssh_node_must_hold_a_plain_checkout(self):
        self.assertEqual(self.scenario("checkout main /n/ws/.git").workspace_layout(self.env("ssh"))["kind"], "checkout")
        with self.assertRaises(PreflightError):
            self.scenario("worktree kandev/pool/e1 /src/.git").workspace_layout(self.env("ssh"))

    def test_empty_or_failed_listing_is_refused(self):
        for output, code in (("", 0), ("worktree kandev/pool/e1 /src/.git", 1)):
            with self.assertRaises(PreflightError):
                self.scenario(output, code).workspace_layout(self.env("local"))

    def test_cleaned_requires_branch_and_registration_gone_for_a_worktree(self):
        layout = {"kind": "worktree", "branch": "kandev/pool/e1", "git_common_dir": "/src/.git"}
        gone = self.scenario("", 1)
        gone.r.ssh = lambda cmd: (1, "") if "show-ref" in cmd else (0, "worktree /src\n")
        self.assertTrue(gone.assert_pool_cleaned(self.env("local"), layout)["branch_removed"])
        branch_left = self.scenario("", 0)
        branch_left.r.ssh = lambda cmd: (0, "")
        with self.assertRaises(PreflightError):
            branch_left.assert_pool_cleaned(self.env("local"), layout)
        registered = self.scenario("", 0)
        registered.r.ssh = lambda cmd: (1, "") if "show-ref" in cmd else (0, "worktree /n/workspaces/environments/e1/g1\n")
        with self.assertRaises(PreflightError):
            registered.assert_pool_cleaned(self.env("local"), layout)


class ActiveNodeRoutingTest(unittest.TestCase):
    def test_commands_follow_the_node_in_use(self):
        runner = fixture.Runner.__new__(fixture.Runner)
        runner.ops = fixture.Ops()
        sent = []
        runner.ops.local_exec = lambda cmd: sent.append(("local", cmd)) or (0, "")
        runner.ssh_remote = lambda cmd: sent.append(("ssh", cmd)) or (0, "")
        runner.active_attach = "ssh"
        runner.save_state = lambda **kw: runner.__dict__.setdefault("saved", []).append(kw)
        runner._exec_on_active_node("a")
        runner.use_node("local", "/n")
        runner._exec_on_active_node("b")
        self.assertEqual(sent, [("ssh", "a"), ("local", "b")])
        self.assertEqual(runner.saved, [{"pool_state_root": "/n"}])
        with self.assertRaises(PreflightError):
            runner.use_node("bogus", "/n")


class PoolSettledTest(unittest.TestCase):
    def scenario(self, existing):
        import nomad_native_e2e_wo07 as wo07
        scn = pool.PoolScenario.__new__(pool.PoolScenario)
        scn.guard = None
        scn.read_state = lambda env: {"workspace_path": "/fx/worker/workspaces/environments/e1/g1"}
        scn.r = type("R", (), {"state": {"pool_state_root": "/fx/worker"},
                               "ssh": lambda self, cmd: (0 if any(p in cmd for p in existing) else 1, "")})()
        base = lambda self, env, pending: {"phase": "absent"}
        self.patch = unittest.mock.patch.object(wo07.Wo07Scenario, "production_settled", base, create=True)
        self.patch.start()
        self.addCleanup(self.patch.stop)
        return scn

    def test_settled_only_when_workspace_and_fence_directory_are_gone(self):
        env = {"environment_id": "e1"}
        done = self.scenario([]).production_settled(env, [])
        self.assertTrue(done["workspace_removed"] and done["fence_directory_removed"])
        pending = []
        self.assertIsNone(self.scenario(["workspaces/environments"]).production_settled(env, pending))
        self.assertIn("workspace", pending[0])
        pending = []
        self.assertIsNone(self.scenario(["/environments/e1'"]).production_settled(env, pending))
        self.assertIn("fence directory", pending[0])


if __name__ == "__main__":
    unittest.main()
