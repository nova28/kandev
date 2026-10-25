import contextlib
import io
import json
import os
import sqlite3
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace

from nomad_native_e2e import (
    PreflightError, REQUIRED_SCENARIOS, _fixture_backend_argv, _pre_ready_contract, _recovery_completed_turn,
    _run_attached_session, check_post_ready_environment, check_pre_ready_environment, check_pre_ready_session,
    main, validate_config,
)


class NomadNativePreflightTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="nomad-e2e-contract-")
        self.root = Path(self.temp.name).resolve()
        self.root.chmod(0o700)
        self.nomad = self._executable("nomad")
        self.ssh = self._executable("ssh")
        self.sshd = self._executable("sshd")
        self.keygen = self._executable("ssh-keygen")
        self.private_key = self._file("id_ed25519", b"fixture-private-key\n", 0o600)
        self.known_hosts = self._file("known_hosts", b"127.0.0.1 ssh-ed25519 fixture\n", 0o600)
        self.kandev_home = self.root / "kandev-home"
        (self.kandev_home / ".ssh").mkdir(parents=True, mode=0o700)
        self.kandev_home.chmod(0o700)
        (self.kandev_home / ".ssh").chmod(0o700)
        (self.kandev_home / ".ssh" / "id_ed25519").write_bytes(self.private_key.read_bytes())
        (self.kandev_home / ".ssh" / "id_ed25519").chmod(0o600)
        (self.kandev_home / "supervisor").mkdir(mode=0o700)
        self.bundle = self.root / "runtime-bundle"
        self.bin_dir = self.bundle / "bin"
        self.bin_dir.mkdir(parents=True, mode=0o700)
        self.kandev_binary = self._executable_at(self.bin_dir / "kandev")
        for helper in ("agentctl", "agentctl-linux-amd64", "agentctl-linux-arm64", "agentctl-darwin-arm64", "agentctl-darwin-amd64"):
            self._executable_at(self.bin_dir / helper)
        self.launch_manifest = self.kandev_home / "supervisor" / "launch.json"
        self.launch_manifest.write_text(json.dumps({
            "home_dir": str(self.kandev_home),
            "backend_executable": str(self.kandev_binary),
            "argv": ["__backend"],
            "port": 43840,
            "env": {
                "HOME": str(self.kandev_home),
                "KANDEV_HOME_DIR": str(self.kandev_home),
                "KANDEV_DATABASE_PATH": str(self.kandev_home / "data" / "kandev.db"),
            },
        }))
        self.launch_manifest.chmod(0o600)
        self.config = {
            "schema_version": 1,
            "fixture_only": True,
            "fixture_root": str(self.root),
            "kandev_home": str(self.kandev_home),
            "backend": {"binary": str(self.kandev_binary), "port": 43840},
            "nomad": {
                "binary": str(self.nomad),
                "version": "2.0.7",
                "server_rpc": "127.0.0.1:26446",
                "client_rpc_proxy": "127.0.0.1:26448",
                "node_name": "ff-fixture-worker",
            },
            "ssh": {
                "client": str(self.ssh),
                "server": str(self.sshd),
                "keygen": str(self.keygen),
                "host": "127.0.0.1",
                "user": "fixture",
                "port": 22422,
                "private_key": str(self.private_key),
                "known_hosts": str(self.known_hosts),
            },
            "partition": {
                "mode": "loopback-tcp-relay",
                "listen": "127.0.0.1:26448",
                "can_drop_active_connections": True,
            },
        }
        self.commands = []

    def tearDown(self):
        self.temp.cleanup()

    def _file(self, name, content, mode):
        path = self.root / name
        path.write_bytes(content)
        path.chmod(mode)
        return path

    def _executable(self, name):
        path = self._file(name, b"#!/bin/sh\nexit 0\n", 0o700)
        return path

    def _executable_at(self, path):
        path.write_bytes(b"#!/bin/sh\nexit 0\n")
        path.chmod(0o700)
        return path

    def _run(self, command, **kwargs):
        self.commands.append(command)
        if command[-1] == "version":
            return SimpleNamespace(returncode=0, stdout="Nomad v2.0.7 (abc)\n", stderr="")
        return SimpleNamespace(returncode=0, stdout="", stderr="OpenSSH_10.3p1\n")

    def test_valid_config_runs_only_local_version_probes(self):
        result = validate_config(self.config, run=self._run)
        self.assertEqual(result["nomad_version"], "2.0.7")
        self.assertEqual(self.commands, [
            ["/usr/bin/codesign", "--verify", "--strict", str(self.bin_dir / "agentctl-darwin-arm64")],
            [str(self.nomad), "version"],
            [str(self.ssh), "-V"],
        ])

    def test_non_loopback_nomad_endpoint_is_rejected_before_probes(self):
        self.config["nomad"]["client_rpc_proxy"] = "192.168.1.20:26448"
        with self.assertRaises(PreflightError):
            validate_config(self.config, run=self._run)
        self.assertEqual(self.commands, [])

    def test_fixture_node_name_is_not_confused_with_runtime_node_uuid(self):
        result = validate_config(self.config, run=self._run)
        self.assertEqual(result["nomad_version"], "2.0.7")
        self.assertNotIn("node_id", self.config["nomad"])

        self.config["nomad"]["node_name"] = "00000000-0000-0000-0000-000000000001"
        with self.assertRaises(PreflightError):
            validate_config(self.config, run=self._run)

    def test_private_key_permissions_are_required(self):
        self.private_key.chmod(0o644)
        with self.assertRaises(PreflightError):
            validate_config(self.config, run=self._run)
        self.assertEqual(self.commands, [])

    def test_backend_home_identity_must_match_pinned_ssh_key(self):
        identity = self.kandev_home / ".ssh" / "id_ed25519"
        identity.write_bytes(b"different-private-key\n")
        identity.chmod(0o600)
        with self.assertRaisesRegex(PreflightError, "differs from the pinned fixture client identity"):
            validate_config(self.config, run=self._run)
        self.assertEqual(self.commands, [])

    def test_backend_home_must_be_private_and_inside_fixture_root(self):
        self.kandev_home.chmod(0o755)
        with self.assertRaisesRegex(PreflightError, "private directory inside fixture_root"):
            validate_config(self.config, run=self._run)
        self.assertEqual(self.commands, [])

    def test_launch_manifest_must_prove_child_home_before_any_probe(self):
        payload = json.loads(self.launch_manifest.read_text())
        payload["env"]["HOME"] = str(self.root)
        self.launch_manifest.write_text(json.dumps(payload))
        self.launch_manifest.chmod(0o600)
        with self.assertRaisesRegex(PreflightError, "launch manifest does not prove fixture HOME"):
            validate_config(self.config, run=self._run)
        self.assertEqual(self.commands, [])

    def test_launch_manifest_is_required_before_any_probe(self):
        self.launch_manifest.unlink()
        with self.assertRaisesRegex(PreflightError, "Kandev launch manifest must exist"):
            validate_config(self.config, run=self._run)
        self.assertEqual(self.commands, [])

    def test_backend_bundle_must_use_adjacent_complete_bin_layout(self):
        self.kandev_binary = self._executable_at(self.root / "kandev")
        self.config["backend"]["binary"] = str(self.kandev_binary)
        with self.assertRaisesRegex(PreflightError, "canonical <bundle>/bin/kandev"):
            from nomad_native_e2e import _verify_runtime_bundle
            _verify_runtime_bundle(self.kandev_binary, self._run)
        self.assertEqual(self.commands, [])

    def test_missing_runtime_helper_fails_before_any_probe(self):
        (self.bin_dir / "agentctl-darwin-arm64").unlink()
        with self.assertRaisesRegex(PreflightError, "missing agentctl-darwin-arm64"):
            validate_config(self.config, run=self._run)
        self.assertEqual(self.commands, [])

    def test_fixture_launcher_exec_argv_contains_binary_once_before_run_args(self):
        argv = _fixture_backend_argv(self.kandev_binary, self.root / "startup.log", 43841)
        self.assertEqual(argv[4:], [str(self.kandev_binary), "run", "--headless", "--port", "43841"])
        self.assertEqual(argv[4:].count(str(self.kandev_binary)), 1)

    def test_non_session_scenario_fails_closed_without_starting_services(self):
        error = io.StringIO()
        with contextlib.redirect_stderr(error):
            result = main(["run", "--scenario", "admission", "--require-real", "--config", "/missing/config.json"])
        self.assertEqual(result, 2)
        self.assertIn("requires --require-real --scenario session", error.getvalue())
        self.assertEqual(self.commands, [])

    def test_recovery_scenario_has_its_own_strict_fixture_gate(self):
        error = io.StringIO()
        with contextlib.redirect_stderr(error):
            result = main(["run", "--scenario", "recovery", "--require-real", "--config", "/missing/config.json"])
        self.assertEqual(result, 2)
        self.assertIn("recovery verification failed", error.getvalue())
        self.assertEqual(self.commands, [])

    def test_attached_real_session_requires_complete_explicit_contract(self):
        with self.assertRaisesRegex(PreflightError, "exact attached-fixture session contract"):
            _run_attached_session({"fixture_root": str(self.root), "session": {}})

    def test_recovery_turn_requires_one_correlated_user_and_agent_message(self):
        turn = {
            "user_message_id": "user-1",
            "agent_delivery_id": "delivery-1",
            "turn_id": "turn-1",
            "prompt_marker": "WO05_MARKER_1",
            "expected_output": "nomad-inert-result",
        }
        messages = [
            {"id": "user-1", "author_type": "user", "type": "message", "turn_id": "turn-1", "content": "output WO05_MARKER_1"},
            {"id": "delivery-1", "author_type": "agent", "type": "message", "turn_id": "turn-1", "content": "nomad-inert-result"},
        ]
        result = _recovery_completed_turn(messages, turn)
        self.assertEqual(result, {"user_message_id": "user-1", "agent_delivery_id": "delivery-1", "turn_id": "turn-1"})

    def test_recovery_turn_allows_prior_user_input_in_same_agent_turn(self):
        turn = {
            "user_message_id": "user-2",
            "agent_delivery_id": "delivery-1",
            "turn_id": "turn-1",
            "prompt_marker": "WO05_MARKER_2",
            "expected_output": "nomad-inert-result",
        }
        messages = [
            {"id": "user-1", "author_type": "user", "type": "message", "turn_id": "turn-1", "content": "initial task input"},
            {"id": "user-2", "author_type": "user", "type": "message", "turn_id": "turn-1", "content": "WO05_MARKER_2"},
            {"id": "delivery-1", "author_type": "agent", "type": "message", "turn_id": "turn-1", "content": "nomad-inert-result"},
        ]
        self.assertEqual(_recovery_completed_turn(messages, turn)["agent_delivery_id"], "delivery-1")

    def test_recovery_turn_rejects_duplicate_or_uncorrelated_delivery(self):
        turn = {
            "user_message_id": "user-1",
            "agent_delivery_id": "delivery-1",
            "turn_id": "turn-1",
            "prompt_marker": "WO05_MARKER_1",
            "expected_output": "nomad-inert-result",
        }
        user = {"id": "user-1", "author_type": "user", "type": "message", "turn_id": "turn-1", "content": "output WO05_MARKER_1"}
        delivery = {"id": "delivery-1", "author_type": "agent", "type": "message", "turn_id": "turn-1", "content": "nomad-inert-result"}
        with self.assertRaisesRegex(PreflightError, "duplicate or unexpected"):
            _recovery_completed_turn([user, delivery, {**delivery, "id": "delivery-2"}], turn)
        with self.assertRaisesRegex(PreflightError, "correlated"):
            _recovery_completed_turn([user, {**delivery, "turn_id": "other-turn"}], turn)


    def _pre_ready_config(self):
        env_id = "11111111-1111-4111-8111-111111111111"
        session = {
            "api_base": "http://127.0.0.1:43840/api/v1", "ws_base": "ws://127.0.0.1:43840", "nomad_http": "http://127.0.0.1:4646",
            "task_id": "22222222-2222-4222-8222-222222222222", "session_id": "33333333-3333-4333-8333-333333333333",
            "environment_id": env_id, "node_id": "44444444-4444-4444-8444-444444444444", "node_name": "ff-fixture-worker",
            "job_id": "job", "allocation_id": "55555555-5555-4555-8555-555555555555", "workspace_path": "/w/g1",
            "environment_generation": 1, "agentctl_pid": 11, "acp_pid": 12, "child_pid": 13, "grandchild_pid": 14,
            "nomad_client_pid": 15, "kandev_backend_pid": 16, "kandev_binary_path": "/bin/kandev",
        }
        expected = {"process_label": "kandev", "operation_id": "op", "server_identity": "srv", "job_create_index": 3,
                    "job_definition_index": 4, "expected_repo_count": 1, "expected_file": "README.md"}
        return {"session": session, "pre_ready_recovery": expected}

    def test_pre_ready_scenario_is_selectable_and_requires_real(self):
        self.assertIn("pre-ready-recovery", REQUIRED_SCENARIOS)
        error = io.StringIO()
        with contextlib.redirect_stderr(error):
            result = main(["run", "--scenario", "pre-ready-recovery", "--config", "/missing/config.json"])
        self.assertEqual(result, 2)
        self.assertIn("requires --require-real", error.getvalue())
        error = io.StringIO()
        with contextlib.redirect_stderr(error):
            result = main(["run", "--scenario", "pre-ready-recovery", "--require-real", "--config", "/missing/config.json"])
        self.assertEqual(result, 2)
        self.assertIn("pre-ready recovery verification failed", error.getvalue())

    def test_config_may_carry_pre_ready_recovery_key(self):
        self.config["pre_ready_recovery"] = self._pre_ready_config()["pre_ready_recovery"]
        self.assertEqual(validate_config(self.config, run=self._run)["nomad_version"], "2.0.7")

    def test_pre_ready_contract_accepts_exact_key_sets(self):
        session, expected = _pre_ready_contract(self._pre_ready_config())
        self.assertEqual(expected["expected_repo_count"], 1)
        self.assertEqual(session["environment_generation"], 1)

    def test_pre_ready_contract_rejects_missing_and_extra_keys(self):
        for section in ("session", "pre_ready_recovery"):
            missing = self._pre_ready_config()
            missing[section].pop(next(iter(missing[section])))
            with self.assertRaises(PreflightError):
                _pre_ready_contract(missing)
            extra = self._pre_ready_config()
            extra[section]["unexpected"] = 1
            with self.assertRaises(PreflightError):
                _pre_ready_contract(extra)
        with self.assertRaises(PreflightError):
            _pre_ready_contract({"session": self._pre_ready_config()["session"]})

    def test_pre_ready_contract_rejects_invalid_values(self):
        for key, value in (("expected_repo_count", -1), ("expected_repo_count", True), ("expected_file", "")):
            config = self._pre_ready_config()
            config["pre_ready_recovery"][key] = value
            with self.assertRaises(PreflightError):
                _pre_ready_contract(config)

    def _environment_db(self, status="creating", workspace_path="", repos=1, generation=1):
        db = sqlite3.connect(":memory:")
        db.execute("CREATE TABLE task_environments (id TEXT PRIMARY KEY, status TEXT, workspace_path TEXT, ownership_generation INTEGER)")
        db.execute("CREATE TABLE task_environment_repos (id TEXT PRIMARY KEY, task_environment_id TEXT)")
        db.execute("INSERT INTO task_environments VALUES ('env', ?, ?, ?)", (status, workspace_path, generation))
        for index in range(repos):
            db.execute("INSERT INTO task_environment_repos VALUES (?, 'env')", (f"r{index}",))
        return db

    def test_pre_ready_environment_requires_creating_with_empty_workspace(self):
        self.assertEqual(check_pre_ready_environment(self._environment_db(), "env"), 1)
        with self.assertRaises(PreflightError):
            check_pre_ready_environment(self._environment_db(status="ready"), "env")
        with self.assertRaises(PreflightError):
            check_pre_ready_environment(self._environment_db(workspace_path="/w/g1"), "env")
        with self.assertRaises(PreflightError):
            check_pre_ready_environment(self._environment_db(), "other")

    def test_post_ready_environment_pending_until_ready_then_exact(self):
        self.assertEqual(check_post_ready_environment(self._environment_db(), "env", "/w/g1", 1, 1), "pending")
        ready = self._environment_db(status="ready", workspace_path="/w/g1")
        self.assertEqual(check_post_ready_environment(ready, "env", "/w/g1", 1, 1), "ready")
        for kwargs, args in (
            ({"status": "ready", "workspace_path": "/w/other"}, ("/w/g1", 1, 1)),
            ({"status": "ready", "workspace_path": "/w/g1", "generation": 2}, ("/w/g1", 1, 1)),
            ({"status": "ready", "workspace_path": "/w/g1", "repos": 0}, ("/w/g1", 1, 1)),
            ({"status": "ready", "workspace_path": "/w/g1", "repos": 2}, ("/w/g1", 1, 1)),
        ):
            with self.assertRaises(PreflightError):
                check_post_ready_environment(self._environment_db(**kwargs), "env", *args)

    def test_pre_ready_session_requires_created_without_acp_or_user_messages(self):
        check_pre_ready_session({"state": "CREATED"}, [{"author_type": "agent", "type": "status"}])
        with self.assertRaises(PreflightError):
            check_pre_ready_session({"state": "WAITING_FOR_INPUT"}, [])
        with self.assertRaises(PreflightError):
            check_pre_ready_session({"state": "CREATED", "downstream_acp_session_id": "acp"}, [])
        with self.assertRaises(PreflightError):
            check_pre_ready_session({"state": "CREATED"}, [{"author_type": "user"}])


if __name__ == "__main__":
    unittest.main()
