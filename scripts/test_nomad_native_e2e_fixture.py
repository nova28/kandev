import contextlib
import io
import json
import os
import sqlite3
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace

import nomad_native_e2e as base
import nomad_native_e2e_bootstrap as bootstrap
import nomad_native_e2e_fixture as fixture
from nomad_native_e2e import PreflightError, main

TOKEN = "s3cr3t-nomad-token-0f9e8d7c"
KEY_BODY = "-----PRIVATE-KEY-BODY-7a6b5c-----"
NODE = "44444444-4444-4444-8444-444444444444"
ENV_ID = "11111111-1111-4111-8111-111111111111"
EXEC_ID = "66666666-6666-4666-8666-666666666666"
ALLOC = "55555555-5555-4555-8555-555555555555"
JOB = "kandev-abc123"
FINGERPRINT = "SHA256:" + "A" * 43
EXECUTOR = "77777777-7777-4777-8777-777777777777"


class FakeClock:
    def __init__(self):
        self.now = 0.0

    def clock(self):
        return self.now

    def sleep(self, seconds):
        self.now += seconds


class FakeRegistry:
    def __init__(self, world):
        self.world = world

    def read_records(self):
        return [dict(item) for item in self.world.records]

    def start(self, label, argv, *, cwd, env):
        w = self.world
        w.pid += 1
        record = {"label": label, "pid": w.pid, "started_at": "t", "pgid": w.pid}
        w.records.append(record)
        w.alive.add(w.pid)
        w.open_ports.add(int(env["KANDEV_SERVER_PORT"]))
        w.starts.append({"argv": list(argv), "env": dict(env), "cwd": str(cwd)})
        w.on_start(env)
        return dict(record)

    def _retire(self, label, result):
        w = self.world
        record = next(item for item in w.records if item["label"] == label)
        w.events.append(result)
        if not (w.stop_leaves_alive and result == "stopped"):
            w.alive.discard(record["pid"])
            w.open_ports.clear()
            w.records = [item for item in w.records if item["label"] != label]
        return result

    def crash(self, label, timeout=5):
        return self._retire(label, "crashed")

    def stop(self, label, timeout=5):
        return self._retire(label, "stopped")


class World:
    """Offline stand-in for the fixture backend, Nomad and the worker."""

    def __init__(self, root_parent):
        self.root_parent = root_parent
        self.pid, self.records, self.alive, self.open_ports, self.starts, self.events = 1000, [], set(), set(), [], []
        self.marker_mode = "normal"
        self.phase, self.captured = "ready", True
        self.recover_mutation = None
        self.stop_leaves_alive = False
        self.stop_effect = True
        self.fence_survives_stop = False
        self.ws_failure = None
        self.workspace_read = {"ok": True, "contains_expected_file": True}
        self.alloc_status = "running"
        self.fence = "owner:fence-content\n"
        self.tree_present = True
        self.job_index = 6
        self.agent_profiles = [{"id": "agent1", "name": "inert"}]
        self.agent_create_profiles = [{"id": "inert-prof", "name": "nomad-fixture-inert"}]
        self.task_created = False
        self.api_calls, self.nomad_paths, self.ws_calls, self.ssh_commands = [], [], [], []
        self.bodies = []
        self.database = None
        self.barrier = None
        self.secret_value = None
        self.clock = FakeClock()

    # backend side effects
    def on_start(self, env):
        self.database = env["KANDEV_DATABASE_PATH"]
        self.barrier = env.get(fixture.BARRIER_ENV)
        if self.task_created and not self.barrier:
            self._recover()

    def _db(self):
        return sqlite3.connect(self.database)

    def _create_task(self):
        self.task_created = True
        state = {
            "environment_id": ENV_ID, "generation": 1, "operation_id": EXEC_ID, "execution_id": EXEC_ID, "job_id": JOB,
            "job_create_index": 5, "job_definition_index": 6, "allocation_id": ALLOC, "node_id": NODE,
            "workspace_path": "/ws/environments/env/g1", "server_identity": "srv", "phase": self.phase, "workspace_input_captured": self.captured,
        }
        db = self._db()
        db.execute("CREATE TABLE executors_running (session_id TEXT, task_id TEXT, runtime TEXT, agent_execution_id TEXT, metadata TEXT)")
        db.execute("CREATE TABLE task_environments (id TEXT, status TEXT, workspace_path TEXT, ownership_generation INTEGER)")
        db.execute("CREATE TABLE task_environment_repos (id TEXT, task_environment_id TEXT)")
        db.execute("INSERT INTO executors_running VALUES ('sess1','task1','nomad',?,?)", (EXEC_ID, json.dumps({"nomad_environment_state": state})))
        db.execute("INSERT INTO task_environments VALUES (?, 'creating', '', 1)", (ENV_ID,))
        db.execute("INSERT INTO task_environment_repos VALUES ('r1', ?)", (ENV_ID,))
        db.commit()
        db.close()
        if self.barrier and self.marker_mode != "none":
            payload = {"execution_id": EXEC_ID, "environment_id": ENV_ID, "session_id": "sess1"}
            if self.marker_mode == "badkeys":
                payload["extra"] = 1
            Path(self.barrier).write_text(json.dumps(payload) + "\n")

    def _recover(self):
        db = self._db()
        db.execute("UPDATE task_environments SET status='ready', workspace_path='/ws/environments/env/g1'")
        db.commit()
        db.close()
        mutation = self.recover_mutation
        if mutation == "job":
            self.job_index = 99
        elif mutation == "fence":
            self.fence = "owner:other\n"
        elif mutation == "tree":
            self.tree_present = "changed"
        elif mutation == "server":
            db = self._db()
            row = json.loads(db.execute("SELECT metadata FROM executors_running").fetchone()[0])
            row["nomad_environment_state"]["server_identity"] = "other"
            db.execute("UPDATE executors_running SET metadata=?", (json.dumps(row),))
            db.commit()
            db.close()

    # injected callables
    def api_factory(self, port):
        def call(method, path, body=None):
            self.api_calls.append((method, path))
            if body is not None:
                self.bodies.append(json.dumps(body))
            if method == "GET" and path == "/workspaces":
                return {"workspaces": [{"id": "ws1"}]}
            if path == "/workspaces/ws1/workflows":
                return {"workflows": [{"id": "wf1"}]}
            if path == "/workflows/wf1/workflow/steps":
                return {"steps": [{"id": "step1"}]}
            if path == "/agents":
                return {"agents": [{"profiles": list(self.agent_profiles)}]}
            if path == "/agents/tui":
                self.agent_created = body
                return {"id": "agent-inert", "profiles": list(self.agent_create_profiles)}
            if path == "/executors" and method == "GET":
                return {"executors": []}
            if path == "/executors":
                return {"id": EXECUTOR}
            if path == "/secrets":
                self.secret_value = body["value"]
                return {"id": "11111111-aaaa-4aaa-8aaa-111111111111"}
            if path == f"/executors/{EXECUTOR}/profiles":
                assert body["config"]["nomad_token"] == "$kandev-secret:11111111-aaaa-4aaa-8aaa-111111111111"
                return {"id": "prof1"}
            if path == "/workspaces/ws1/repositories":
                return {"id": "repo1"}
            if path == "/tasks" and method == "POST":
                self._create_task()
                return {"id": "task1", "session_id": "sess1"}
            if path == "/tasks/task1/sessions":
                return {"sessions": [{"id": "sess1", "state": "CREATED"}]}
            if path == "/task-sessions/sess1/messages":
                return {"messages": [{"id": "m1", "type": "status", "author_type": "agent", "turn_id": "t"}]}
            raise AssertionError(f"unexpected API call {method} {path}")
        return call

    def nomad_factory(self, address, token, namespace, region):
        assert token == TOKEN

        def call(path):
            self.nomad_paths.append(path)
            if path == f"/v1/node/{NODE}":
                return {"ID": NODE, "Status": "ready", "SchedulingEligibility": "eligible"}
            if path == f"/v1/job/{JOB}":
                return {"ID": JOB, "Status": "running", "CreateIndex": 5, "JobModifyIndex": self.job_index}
            if path == f"/v1/job/{JOB}/allocations":
                return [{"ID": ALLOC}]
            if path == f"/v1/allocation/{ALLOC}":
                return {"ID": ALLOC, "NodeID": NODE, "ClientStatus": self.alloc_status, "TaskStates": {"agentctl": {"State": "running"}}}
            raise AssertionError(f"unexpected Nomad read {path}")
        return call

    scanned = None

    def keyscan(self, host, port):
        self.scans = getattr(self, "scans", []) + [(host, port)]
        return ["worker.example ssh-ed25519 GOOD"] if self.scanned is None else self.scanned

    def key_fingerprint(self, line):
        return FINGERPRINT if line.endswith("GOOD") else "SHA256:" + "Z" * 43

    def ssh_factory(self, ssh):
        self.ssh_cfg = ssh
        assert ssh["identity_file"].endswith("/home/.ssh/id_ed25519")

        def call(command):
            self.ssh_commands.append(command)
            if "active-environment" in command:
                return (3, "") if self.fence is None else (0, self.fence)
            assert command.startswith("ps -eww")
            if not self.tree_present:
                return 0, ""
            started = "Sun Oct 25 10:00:00 2026" if self.tree_present is True else "Sun Oct 25 10:09:09 2026"
            return 0, f"  200 1 200 {started} /alloc/{ALLOC}/agentctl\n  201 200 200 {started} /alloc/{ALLOC}/acp\n  1 0 1 Sun Oct 25 09:00:00 2026 /sbin/init\n"
        return call

    def ws(self, payload):
        self.ws_calls.append(payload["mode"])
        if payload["mode"] == "workspace-read":
            if self.ws_failure:
                raise PreflightError("fixture WebSocket driver workspace-read failed")
            return self.workspace_read
        if self.stop_effect:
            self.alloc_status = "complete"
        if not self.fence_survives_stop:
            self.fence = None
        self.tree_present = False
        return {"ok": True}

    def ops(self):
        return fixture.Ops(
            free_port=lambda: 43111, port_open=lambda port: port in self.open_ports, pid_alive=lambda pid: pid in self.alive,
            listener_pids=lambda port: [r["pid"] for r in self.records] if port in self.open_ports else [],
            registry=lambda root: FakeRegistry(self), api_factory=self.api_factory, nomad_factory=self.nomad_factory,
            keyscan=self.keyscan, key_fingerprint=self.key_fingerprint, ssh_factory=self.ssh_factory, ws=self.ws, clock=self.clock.clock, sleep=self.clock.sleep, wall=lambda: "2026-01-01T00:00:00+00:00",
        )


class FixtureBase(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="nomad-e2e-fixture-test-")
        self.addCleanup(self.temp.cleanup)
        self.tmp = Path(self.temp.name).resolve()
        self.checkout = self._dir("checkout")
        self.parent = self._dir("fixtures")
        bundle = self._dir("bundle/bin")
        for name in ("kandev", "agentctl", "agentctl-linux-amd64", "agentctl-linux-arm64", "agentctl-darwin-arm64", "agentctl-darwin-amd64"):
            (bundle / name).write_bytes(b"#!/bin/sh\n")
            (bundle / name).chmod(0o700)
        self.binary = bundle / "kandev"
        self.token_file = self._file("nomad.token", TOKEN + "\n")
        self.identity = self._file("id_ed25519", KEY_BODY + "\n")
        self.repo = self._dir("repo-fixture")
        (self.repo / ".git").mkdir()
        (self.repo / "README.md").write_text("hello")
        self.config = {
            "schema_version": 1, "kandev_binary": str(self.binary),
            "nomad": {"address": "http://127.0.0.1:4646", "token_file": str(self.token_file), "namespace": "default", "region": "global", "datacenter": "dc1", "node_id": NODE, "platform": "linux/amd64"},
            "ssh": {"target": "fixture@worker.example", "port": 22, "host_fingerprint": FINGERPRINT, "identity_file": str(self.identity)},
            "workspace_root": "/srv/kandev/workspace", "allocation_root": "/srv/kandev/nomad/alloc", "cpu_mhz": 500, "memory_mb": 512,
            "repository": {"path": str(self.repo), "base_branch": "main"}, "expected_file": "README.md", "temp_root_parent": str(self.parent),
        }
        self.world = World(self.parent)
        self.out, self.err = [], []

    def _dir(self, name):
        path = self.tmp / name
        path.mkdir(parents=True, mode=0o700)
        return path

    def _file(self, name, text):
        path = self.tmp / name
        path.write_text(text)
        path.chmod(0o600)
        return path

    def run_flow(self, config=None):
        return fixture.run_create_fixture(
            self.config if config is None else config, ops=self.world.ops(), out=self.out.append, err=self.err.append,
            run=lambda *a, **k: SimpleNamespace(returncode=0, stdout="", stderr=""), checkout_root=self.checkout,
        )

    def validate(self, config=None):
        return bootstrap.validate_bootstrap(
            self.config if config is None else config, run=lambda *a, **k: SimpleNamespace(returncode=0, stdout="", stderr=""), checkout_root=self.checkout,
        )

    def fixture_root(self):
        roots = [p for p in self.parent.iterdir() if p.name.startswith("nomad-e2e-fixture-")]
        self.assertEqual(len(roots), 1)
        return roots[0]

    def evidence(self):
        return json.loads((self.fixture_root() / "evidence.json").read_text())

    def everything_written(self):
        chunks = ["\n".join(self.out), "\n".join(self.err), "\n".join(self.world.bodies[:0])]
        for path in self.fixture_root().rglob("*"):
            if path.is_file() and path.name != "id_ed25519":
                chunks.append(path.read_text(errors="replace"))
        return "\n".join(chunks)


class BootstrapContractTest(FixtureBase):
    def test_valid_bootstrap_normalizes_without_reading_token_into_output(self):
        result = self.validate()
        self.assertEqual(result["nomad"]["address"], "http://127.0.0.1:4646")
        self.assertNotIn(TOKEN, json.dumps(result))
        self.assertEqual(bootstrap.read_token(Path(result["nomad"]["token_file"])), TOKEN)

    def test_key_set_is_strict_at_every_level(self):
        for path in ((), ("nomad",), ("ssh",), ("repository",)):
            for mutation in ("missing", "extra"):
                config = json.loads(json.dumps(self.config))
                target = config
                for key in path:
                    target = target[key]
                if mutation == "missing":
                    target.pop(next(k for k in target if k != "base_branch"))
                else:
                    target["unexpected"] = 1
                with self.assertRaises(PreflightError, msg=f"{path} {mutation}"):
                    self.validate(config)

    def test_optional_agent_profile_name_is_accepted(self):
        self.config["agent_profile_name"] = "inert"
        self.assertEqual(self.validate()["agent_profile_name"], "inert")
        self.config["agent_profile_name"] = " "
        with self.assertRaises(PreflightError):
            self.validate()

    def test_repository_inside_checkout_is_refused(self):
        inside = self.checkout / "fixtures-repo"
        (inside / ".git").mkdir(parents=True)
        (inside / "README.md").write_text("x")
        self.config["repository"]["path"] = str(inside)
        with self.assertRaisesRegex(PreflightError, "outside the Kandev checkout"):
            self.validate()

    def test_temp_root_parent_inside_checkout_or_git_repo_is_refused(self):
        inside = self.checkout / "tmp"
        inside.mkdir()
        self.config["temp_root_parent"] = str(inside)
        with self.assertRaisesRegex(PreflightError, "outside the Kandev checkout"):
            self.validate()
        other = self._dir("other-repo")
        (other / ".git").mkdir()
        nested = other / "nested"
        nested.mkdir(mode=0o700)
        self.config["temp_root_parent"] = str(nested)
        with self.assertRaisesRegex(PreflightError, "git repository"):
            self.validate()

    def test_temp_root_parent_must_not_be_group_writable(self):
        self.parent.chmod(0o770)
        with self.assertRaises(PreflightError):
            self.validate()

    def test_nomad_address_contract(self):
        for good in ("http://127.0.0.1:4646", "http://localhost:4646", "https://nomad.example.com:4646", "https://10.1.2.3:4646/"):
            bootstrap.nomad_address(good)
        for bad in ("http://nomad.example.com:4646", "http://10.1.2.3:4646", "https://nomad.example.com", "ftp://127.0.0.1:1", "https://u:p@nomad.example.com:4646",
                    "https://nomad.example.com:4646/v1", "https://nomad.example.com:4646?x=1", "nomad.example.com:4646", "https://nomad.example.com:99999", 5):
            with self.assertRaises(PreflightError, msg=str(bad)):
                bootstrap.nomad_address(bad)

    def test_token_and_identity_files_must_be_private(self):
        self.token_file.chmod(0o640)
        with self.assertRaises(PreflightError):
            self.validate()
        self.token_file.chmod(0o600)
        self.identity.chmod(0o644)
        with self.assertRaises(PreflightError):
            self.validate()

    def test_token_file_must_hold_a_single_token(self):
        self.token_file.write_text("two words\n")
        with self.assertRaises(PreflightError):
            bootstrap.read_token(self.token_file)

    def test_other_invalid_values(self):
        cases = (
            (("ssh", "host_fingerprint"), "MD5:aa"), (("ssh", "target"), "-oProxyCommand=x"), (("ssh", "port"), 0),
            (("nomad", "node_id"), "not-a-uuid"), (("nomad", "platform"), "windows/amd64"), (("nomad", "namespace"), "bad ns"),
            (("workspace_root",), "relative/path"), (("workspace_root",), "/a/../b"), (("allocation_root",), "/a"), (("cpu_mhz",), True),
            (("memory_mb",), 0), (("expected_file",), "/etc/passwd"), (("expected_file",), "../x"), (("expected_file",), "missing.txt"),
            (("repository", "base_branch"), "-bad"), (("schema_version",), 2), (("kandev_binary",), "relative/kandev"),
        )
        for path, value in cases:
            config = json.loads(json.dumps(self.config))
            target = config
            for key in path[:-1]:
                target = target[key]
            target[path[-1]] = value
            with self.assertRaises(PreflightError, msg=str(path)):
                self.validate(config)

    def test_repository_url_form(self):
        self.config["repository"] = {"url": "https://example.com/org/repo.git", "base_branch": "main"}
        self.assertIn("url", self.validate()["repository"])
        self.config["repository"] = {"url": "https://user:pw@example.com/org/repo.git", "base_branch": "main"}
        with self.assertRaises(PreflightError):
            self.validate()
        self.config["repository"] = {"path": str(self.repo), "url": "https://example.com/x", "base_branch": "main"}
        with self.assertRaises(PreflightError):
            self.validate()

    def test_runtime_bundle_must_be_complete(self):
        (self.binary.parent / "agentctl-linux-arm64").unlink()
        with self.assertRaises(PreflightError):
            self.validate()


class CreateFlowTest(FixtureBase):
    def test_live_port_is_refused_before_anything_is_created(self):
        ops = self.world.ops()
        ops.free_port = lambda: 8817
        code = fixture.run_create_fixture(self.config, ops=ops, out=self.out.append, err=self.err.append, run=lambda *a, **k: SimpleNamespace(returncode=0, stdout="", stderr=""), checkout_root=self.checkout)
        self.assertEqual(code, 2)
        self.assertIn("live Kandev port", "\n".join(self.err))
        self.assertEqual(list(self.parent.iterdir()), [])
        self.assertEqual(self.world.starts, [])

    def test_invalid_bootstrap_creates_nothing_and_prints_no_cleanup_command(self):
        self.config["unexpected"] = 1
        self.assertEqual(self.run_flow(), 2)
        self.assertEqual(list(self.parent.iterdir()), [])
        self.assertNotIn("cleanup-fixture", "\n".join(self.out + self.err))

    def test_successful_flow_writes_evidence_and_never_leaks_secrets(self):
        self.assertEqual(self.run_flow(), 0, self.err)
        root = self.fixture_root()
        self.assertEqual(root.stat().st_mode & 0o777, 0o700)
        evidence = self.evidence()
        self.assertEqual(evidence["status"], "passed")
        self.assertEqual([s["status"] for s in evidence["steps"]], ["passed"] * len(evidence["steps"]))
        self.assertEqual([s["name"] for s in evidence["steps"]], [
            "start-backend-with-barrier", "create-fixture-objects", "await-pre-publish-barrier", "snapshot-before-crash", "crash-backend",
            "restart-backend-without-barrier", "await-recovery", "assert-identity-preserved", "workspace-read-before-any-prompt", "cleanup-production-stop-path",
        ])
        self.assertEqual((evidence["job_id"], evidence["allocation_id"], evidence["node_id"], evidence["execution_id"]), (JOB, ALLOC, NODE, EXEC_ID))
        self.assertEqual(evidence["environment_status"], {"before": "creating", "after": "ready"})
        self.assertEqual(evidence["inventory_phase"], {"before": "ready", "after": "ready"})
        self.assertEqual(evidence["cleanup"]["allocation_state"], "complete")
        self.assertTrue(evidence["cleanup"]["fence_released"])
        self.assertEqual(evidence["prompts_sent"], 0)
        self.assertTrue(any(line == f"evidence: {root / 'evidence.json'}" for line in self.out))
        text = self.everything_written()
        self.assertNotIn(TOKEN, text)
        self.assertNotIn(KEY_BODY, text)
        self.assertNotIn(str(self.identity), text)
        self.assertNotIn(str(self.token_file), evidence and json.dumps(evidence))
        self.assertEqual(self.world.secret_value, TOKEN)
        self.assertFalse(any(TOKEN in body for body in self.world.bodies if "nomad_token" in body))

    def test_process_and_port_contract(self):
        self.assertEqual(self.run_flow(), 0, self.err)
        first, second = self.world.starts
        self.assertIn(fixture.BARRIER_ENV, first["env"])
        self.assertTrue(first["env"][fixture.BARRIER_ENV].startswith(str(self.fixture_root())))
        self.assertNotIn(fixture.BARRIER_ENV, second["env"])
        for start in (first, second):
            self.assertEqual(start["env"][fixture.FIXTURE_ONLY_ENV], "1")
            self.assertEqual(start["env"]["KANDEV_SERVER_PORT"], "43111")
            self.assertNotEqual(start["env"]["KANDEV_SERVER_PORT"], "8817")
            self.assertEqual(start["argv"], [str(self.binary), "__backend"])
            self.assertEqual(start["cwd"], str(self.fixture_root() / "home"))
        self.assertEqual(self.world.events, ["crashed", "stopped"])
        self.assertEqual(self.world.records, [])
        self.assertEqual(self.world.open_ports, set())

    def test_launch_manifest_is_consistent_with_the_attached_verifier(self):
        self.assertEqual(self.run_flow(), 0, self.err)
        root = self.fixture_root()
        base._verify_backend_launch_manifest(root, root / "home", {"binary": str(self.binary), "port": 43111})
        key = root / "home" / ".ssh" / "id_ed25519"
        self.assertEqual(key.stat().st_mode & 0o777, 0o600)
        self.assertEqual(key.read_text(), KEY_BODY + "\n")

    def test_runner_only_reads_nomad_and_never_stops_jobs(self):
        self.assertEqual(self.run_flow(), 0, self.err)
        self.assertTrue(self.world.nomad_paths)
        self.assertTrue(all(path.startswith("/v1/") and not path.startswith("/v1/job/" + JOB + "/") or path.endswith("/allocations") for path in self.world.nomad_paths))
        source = (Path(fixture.__file__).read_text() + Path(base.__file__).read_text()).lower()
        self.assertNotIn("job stop", source)
        self.assertNotIn("deregister", source)

    def test_workspace_read_happens_before_the_production_stop(self):
        self.assertEqual(self.run_flow(), 0, self.err)
        self.assertEqual(self.world.ws_calls, ["workspace-read", "session-stop"])

    def test_profile_is_created_through_the_payload_builder_with_a_secret_reference(self):
        self.assertEqual(self.run_flow(), 0, self.err)
        self.assertIn(("POST", "/secrets"), self.world.api_calls)
        profile = json.loads(next(b for b in self.world.bodies if "ssh_host_fingerprint" in b))
        self.assertEqual(profile["config"]["cpu_mhz"], "500")
        self.assertTrue(profile["config"]["nomad_token"].startswith("$kandev-secret:"))
        task = json.loads(next(b for b in self.world.bodies if "prepare_session" in b))
        self.assertTrue(task["prepare_session"])
        self.assertNotIn("start_agent", task)


class AgentProfileSelectionTest(FixtureBase):
    def task_body(self):
        return json.loads(next(b for b in self.world.bodies if "prepare_session" in b))

    def test_zero_profiles_creates_the_inert_acp_agent_and_uses_its_profile(self):
        self.world.agent_profiles = []
        self.assertEqual(self.run_flow(), 0, self.err)
        created = self.world.agent_created
        self.assertEqual(created["protocol"], "acp")
        self.assertEqual(created["command"], "python3")
        self.assertEqual(created["command_args"], [str(fixture.INERT_ACP_SCRIPT)])
        self.assertTrue(fixture.INERT_ACP_SCRIPT.is_file())
        self.assertEqual(created["display_name"], fixture.INERT_AGENT_NAME)
        self.assertEqual(self.task_body()["agent_profile_id"], "inert-prof")

    def test_zero_matching_profiles_creates_the_agent_under_the_requested_name(self):
        self.config["agent_profile_name"] = "other"
        self.assertEqual(self.run_flow(), 0, self.err)
        self.assertEqual(self.world.agent_created["display_name"], "other")

    def test_exactly_one_profile_is_used_without_creating_an_agent(self):
        self.assertEqual(self.run_flow(), 0, self.err)
        self.assertNotIn(("POST", "/agents/tui"), self.world.api_calls)
        self.assertEqual(self.task_body()["agent_profile_id"], "agent1")

    def test_many_profiles_without_a_name_fail_without_creating_anything(self):
        self.world.agent_profiles = [{"id": "a", "name": "x"}, {"id": "b", "name": "y"}]
        self.assertEqual(self.run_flow(), 2)
        self.assertNotIn(("POST", "/agents/tui"), self.world.api_calls)
        self.assertNotIn(("POST", "/tasks"), self.world.api_calls)

    def test_a_name_selects_one_of_many_profiles(self):
        self.world.agent_profiles = [{"id": "a", "name": "x"}, {"id": "b", "name": "y"}]
        self.config["agent_profile_name"] = "y"
        self.assertEqual(self.run_flow(), 0, self.err)
        self.assertEqual(self.task_body()["agent_profile_id"], "b")

    def test_inert_agent_creation_without_exactly_one_profile_fails_closed(self):
        self.world.agent_profiles = []
        self.world.agent_create_profiles = []
        self.assertNotEqual(self.run_flow(), 0)
        self.assertNotIn(("POST", "/tasks"), self.world.api_calls)


class FailureFlowTest(FixtureBase):
    def assertFailedLeavingFixture(self, step, message=None):
        self.assertEqual(self.run_flow(), 2)
        evidence = self.evidence()
        self.assertEqual(evidence["status"], "failed")
        self.assertEqual(evidence["failed_step"], step)
        if message:
            self.assertIn(message, evidence["error"])
        root = self.fixture_root()
        self.assertIn(f"operator cleanup: python3 scripts/nomad_native_e2e.py cleanup-fixture --fixture-root {root}", self.out)
        self.assertNotIn(TOKEN, self.everything_written())
        return evidence

    def test_marker_never_appears_is_a_bounded_failure_without_cleanup(self):
        self.world.marker_mode = "none"
        self.assertFailedLeavingFixture("await-pre-publish-barrier", "did not appear")
        self.assertLessEqual(self.world.clock.now, fixture.MARKER_TIMEOUT + 1)
        self.assertEqual(self.world.ws_calls, [])
        self.assertEqual(self.world.events, [])
        self.assertEqual(len(self.world.records), 1)

    def test_malformed_marker_is_a_failure(self):
        self.world.marker_mode = "badkeys"
        self.assertFailedLeavingFixture("await-pre-publish-barrier", "exactly execution_id")

    def test_inventory_phase_wrong_fails_before_the_crash(self):
        self.world.phase = "preparing"
        self.assertFailedLeavingFixture("await-pre-publish-barrier", "phase ready")
        self.assertEqual(self.world.events, [])

    def test_inventory_input_not_captured_fails_before_the_crash(self):
        self.world.captured = False
        self.assertFailedLeavingFixture("await-pre-publish-barrier", "workspace input captured")
        self.assertEqual(self.world.events, [])

    def test_identity_changes_after_restart_are_detected_and_nothing_is_cleaned(self):
        for mutation, message in (("job", "Nomad job or allocation"), ("fence", "worker fence"), ("tree", "process tree"), ("server", "execution, job, allocation, or server")):
            with self.subTest(mutation):
                self.setUp()
                self.world.recover_mutation = mutation
                self.assertFailedLeavingFixture("assert-identity-preserved", message)
                self.assertEqual(self.world.ws_calls, [])
                self.assertEqual(self.world.events, ["crashed"])
                self.assertEqual(len(self.world.records), 1)
                self.temp.cleanup()

    def test_workspace_read_failure_leaves_the_fixture(self):
        self.world.ws_failure = True
        self.assertFailedLeavingFixture("workspace-read-before-any-prompt", "workspace-read failed")
        self.assertEqual(self.world.ws_calls, ["workspace-read"])
        self.assertEqual(self.world.events, ["crashed"])

    def test_workspace_read_missing_expected_file_fails(self):
        self.world.workspace_read = {"ok": True, "contains_expected_file": False}
        self.assertFailedLeavingFixture("workspace-read-before-any-prompt", "expected file")

    def test_recovery_that_never_becomes_ready_times_out(self):
        self.world.recover_mutation = None
        original = self.world._recover
        self.world._recover = lambda: None
        self.assertFailedLeavingFixture("await-recovery", "did not become ready")
        self.assertLessEqual(self.world.clock.now, 130)
        self.world._recover = original

    def test_cleanup_failures_fail_loudly(self):
        cases = (
            ("allocation not terminal", {"stop_effect": False}, "terminal state"),
            ("fence still present", {"fence_survives_stop": True}, "fence is still present"),
            ("process still alive", {"stop_leaves_alive": True}, "still alive"),
        )
        for name, attrs, message in cases:
            with self.subTest(name):
                self.setUp()
                for key, value in attrs.items():
                    setattr(self.world, key, value)
                self.assertFailedLeavingFixture("cleanup-production-stop-path", message)
                self.assertEqual(self.world.ws_calls.count("session-stop"), 1)
                self.temp.cleanup()

    def test_secrets_in_error_text_are_scrubbed(self):
        evidence = fixture.Evidence(self.parent, [TOKEN])
        self.assertNotIn(TOKEN, evidence.scrub(f"boom {TOKEN} boom"))


class HostKeyPinningTest(FixtureBase):
    def test_matching_key_is_written_alone_and_used_strictly(self):
        self.world.scanned = ["worker.example ssh-ed25519 OTHER", "worker.example ssh-ed25519 GOOD"]
        self.assertEqual(self.run_flow(), 0, self.err)
        known = (self.fixture_root() / "known_hosts")
        self.assertEqual(known.read_text(), "worker.example ssh-ed25519 GOOD\n")
        self.assertEqual(known.stat().st_mode & 0o777, 0o600)
        self.assertEqual(self.world.ssh_cfg["known_hosts"], str(known))
        self.assertEqual(self.world.scans[0], ("worker.example", 22))

    def test_default_ssh_command_is_strict(self):
        calls = []
        original = fixture.subprocess.run
        fixture.subprocess.run = lambda cmd, **kw: calls.append(cmd) or SimpleNamespace(returncode=0, stdout="", stderr="")
        try:
            fixture._default_ssh({"identity_file": "/k", "known_hosts": "/kh", "port": 22, "target": "u@h"})("true")
        finally:
            fixture.subprocess.run = original
        cmd = calls[0]
        self.assertIn("StrictHostKeyChecking=yes", cmd)
        self.assertIn("UserKnownHostsFile=/kh", cmd)
        self.assertIn("BatchMode=yes", cmd)
        self.assertFalse(any("DEBUG" in part or "=no" in part for part in cmd))

    def test_mismatch_or_empty_scan_fails_before_any_remote_command(self):
        for scanned in (["worker.example ssh-ed25519 OTHER"], []):
            self.setUp()
            self.world.scanned = scanned
            self.assertEqual(self.run_flow(), 2)
            self.assertEqual(self.world.ssh_commands, [])
            self.assertIn("pinned fingerprint", "\n".join(self.err))
            self.temp.cleanup()


class CleanupFixtureTest(FixtureBase):
    def cleanup(self, root=None):
        return fixture.run_cleanup_fixture(str(root or self.fixture_root()), ops=self.world.ops(), out=self.out.append, err=self.err.append)

    def test_cleanup_after_an_assertion_failure_uses_the_production_stop_path(self):
        self.world.recover_mutation = "fence"
        self.assertEqual(self.run_flow(), 2)
        self.world.fence = "owner:fence-content\n"
        self.out.clear()
        self.assertEqual(self.cleanup(), 0, self.err)
        evidence = self.evidence()
        self.assertEqual(evidence["status"], "failed")
        self.assertEqual(evidence["cleanup"]["status"], "passed")
        self.assertEqual(self.world.ws_calls, ["session-stop"])
        self.assertEqual(self.world.records, [])
        self.assertEqual(self.world.alloc_status, "complete")
        self.assertNotIn(TOKEN, self.everything_written())

    def test_cleanup_starts_the_backend_first_when_it_is_down(self):
        self.world.marker_mode = "none"
        self.assertEqual(self.run_flow(), 2)
        FakeRegistry(self.world).crash(fixture.PROCESS_LABEL)
        self.world.open_ports.clear()
        starts_before = len(self.world.starts)
        self.assertEqual(self.cleanup(), 0, self.err)
        self.assertEqual(len(self.world.starts), starts_before + 1)
        self.assertNotIn(fixture.BARRIER_ENV, self.world.starts[-1]["env"])

    def test_cleanup_failure_fails_loudly_once_and_reprints_the_command(self):
        self.world.recover_mutation = "tree"
        self.assertEqual(self.run_flow(), 2)
        self.world.stop_effect = False
        self.out.clear()
        self.assertEqual(self.cleanup(), 2)
        self.assertEqual(self.world.ws_calls.count("session-stop"), 1)
        self.assertTrue(any(line.startswith("operator cleanup: python3 scripts/nomad_native_e2e.py cleanup-fixture") for line in self.out))
        self.assertEqual(self.evidence()["cleanup"]["status"], "failed")

    def test_cleanup_without_a_fixture_state_fails_before_acting(self):
        empty = self._dir("empty-root")
        self.assertEqual(self.cleanup(empty), 2)
        self.assertEqual(self.world.ws_calls, [])
        self.assertEqual(self.world.starts, [])

    def test_cli_wiring(self):
        err = io.StringIO()
        with contextlib.redirect_stderr(err):
            self.assertEqual(main(["cleanup-fixture", "--fixture-root", str(self.tmp / "missing")]), 2)
            self.assertEqual(main(["run", "--scenario", "session", "--require-real", "--create-fixture", "--config", "x"]), 2)
            self.assertEqual(main(["run", "--scenario", "pre-ready-recovery", "--create-fixture", "--config", "x"]), 2)
            self.assertEqual(main(["run", "--scenario", "pre-ready-recovery", "--require-real", "--create-fixture", "--config", str(self.tmp / "nope.json")]), 2)
        self.assertIn("cleanup-fixture failed before any action", err.getvalue())
        self.assertIn("--create-fixture requires", err.getvalue())
        self.assertIn("config is unreadable", err.getvalue())


class SharedHelpersTest(unittest.TestCase):
    def test_await_recovered_environment_times_out_on_a_fake_clock(self):
        clock = FakeClock()
        db = sqlite3.connect(":memory:")
        db.execute("CREATE TABLE task_environments (id TEXT, status TEXT, workspace_path TEXT, ownership_generation INTEGER)")
        db.execute("CREATE TABLE task_environment_repos (id TEXT, task_environment_id TEXT)")
        db.execute("INSERT INTO task_environments VALUES ('env','creating','',1)")
        with self.assertRaisesRegex(PreflightError, "did not become ready"):
            base._await_recovered_environment(lambda fn: fn(db), lambda: {"workspace_path": "/w"}, environment_id="env", generation_before=1,
                                              expected_repo_count=0, timeout=2, clock=clock.clock, sleep=clock.sleep)
        db.execute("UPDATE task_environments SET status='ready', workspace_path='/w'")
        self.assertEqual(base._await_recovered_environment(lambda fn: fn(db), lambda: {"workspace_path": "/w"}, environment_id="env", generation_before=1,
                                                           expected_repo_count=0, timeout=2, clock=clock.clock, sleep=clock.sleep), {"workspace_path": "/w"})

    def test_session_untouched_and_workspace_read_checks(self):
        messages = [{"id": "a", "type": "status", "turn_id": "t"}]
        base._check_session_untouched({"state": "CREATED"}, messages, messages)
        with self.assertRaises(PreflightError):
            base._check_session_untouched({"state": "CREATED"}, messages + [{"id": "b"}], messages)
        base._check_workspace_read({"ok": True, "contains_expected_file": True})
        with self.assertRaises(PreflightError):
            base._check_workspace_read({"ok": True})


if __name__ == "__main__":
    unittest.main()
