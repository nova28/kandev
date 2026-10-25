"""Create-and-crash driver for the native Nomad pre-ready recovery scenario.

One invocation creates a fresh private fixture, runs its own headless Kandev
through normal APIs, SIGKILLs the backend at the deterministic pre-publish
barrier, restarts it, asserts recovery, and tears down through the production
session stop path. The first failed assertion stops the run without cleanup;
`cleanup-fixture` then performs the teardown against the retained fixture.

The runner never mutates Nomad: its only Nomad access is HTTP GET.
"""

from __future__ import annotations

import hashlib
import importlib.util
import json
import os
import posixpath
import re
import shlex
import socket
import sqlite3
import stat
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Callable

import nomad_native_e2e as base
import nomad_native_e2e_bootstrap as bootstrap
from nomad_native_e2e import PreflightError
from nomad_profile_payload import build_profile_payload, validate_profile_source

PROCESS_LABEL = "kandev-fixture"
BARRIER_ENV = "KANDEV_FIXTURE_NOMAD_PRE_PUBLISH_BARRIER"
FIXTURE_ONLY_ENV = "KANDEV_FIXTURE_ONLY"
MARKER_KEYS = {"execution_id", "environment_id", "session_id"}
MARKER_TIMEOUT = 180.0
API_READY_TIMEOUT = 60.0
TERMINAL_ALLOCATION_STATES = {"complete", "failed", "lost"}
STATE_FILE = "fixture.json"
EVIDENCE_FILE = "evidence.json"
_INVENTORY_KEYS = (
    "environment_id", "generation", "operation_id", "execution_id", "job_id", "job_create_index",
    "job_definition_index", "allocation_id", "node_id", "workspace_path", "server_identity", "phase",
)
_JOB_ID = re.compile(r"kandev-[A-Za-z0-9]+")
_PS_ROW = re.compile(r"\s*(\d+)\s+(\d+)\s+(\d+)\s+(\w{3}\s+\w{3}\s+\d+\s+[\d:]{8}\s+\d{4})\s+(.*)$")


def cleanup_command(root: Path | str) -> str:
    return f"python3 scripts/nomad_native_e2e.py cleanup-fixture --fixture-root {shlex.quote(str(root))}"


def _fixture_argv(binary: str) -> list[str]:
    return [binary, "__backend"]


def _default_free_port() -> int:
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def _default_port_open(port: int) -> bool:
    with socket.socket() as probe:
        probe.settimeout(0.5)
        return probe.connect_ex(("127.0.0.1", port)) == 0


def _default_pid_alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


def _default_listener_pids(port: int) -> list[int]:
    result = subprocess.run(["/usr/sbin/lsof", "-nP", f"-iTCP:{port}", "-sTCP:LISTEN", "-t"], capture_output=True, text=True, timeout=5, check=False)
    return [int(line) for line in result.stdout.splitlines() if line.isdigit()]


def _default_registry(root: Path) -> Any:
    spec = importlib.util.spec_from_file_location("owned_processes", Path(__file__).with_name("owned_processes.py"))
    if spec is None or spec.loader is None:
        raise PreflightError("fixture process registry is unavailable")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module.OwnedProcessRegistry(root)


def _default_read_db(database: str, fn: Callable[[sqlite3.Connection], Any]) -> Any:
    db = sqlite3.connect(f"file:{database}?mode=ro", uri=True, timeout=3)
    try:
        return fn(db)
    finally:
        db.close()


def _default_api(port: int) -> Callable[[str, str, Any], Any]:
    prefix = f"http://127.0.0.1:{port}/api/v1"

    def call(method: str, path: str, body: Any = None) -> Any:
        data = None if body is None else json.dumps(body).encode()
        request = urllib.request.Request(prefix + path, data=data, method=method, headers={"Accept": "application/json", "Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                raw = response.read()
        except urllib.error.HTTPError as exc:
            raise PreflightError(f"fixture API {method} {path.split('?')[0]} failed with status {exc.code}") from None
        except (OSError, urllib.error.URLError):
            raise PreflightError(f"fixture API {method} {path.split('?')[0]} is unreachable") from None
        return json.loads(raw) if raw else {}

    return call


def _default_nomad_get(address: str, token: str, namespace: str, region: str) -> Callable[[str], Any]:
    def call(path: str) -> Any:
        sep = "&" if "?" in path else "?"
        request = urllib.request.Request(f"{address}{path}{sep}namespace={namespace}&region={region}", method="GET", headers={"X-Nomad-Token": token, "Accept": "application/json"})
        try:
            with urllib.request.urlopen(request, timeout=10) as response:
                return json.loads(response.read())
        except urllib.error.HTTPError as exc:
            raise PreflightError(f"Nomad GET {path.split('?')[0]} failed with status {exc.code}") from None
        except (OSError, urllib.error.URLError):
            raise PreflightError(f"Nomad GET {path.split('?')[0]} is unreachable") from None

    return call


def _default_keyscan(host: str, port: int) -> list[str]:
    result = subprocess.run(["/usr/bin/ssh-keyscan", "-p", str(port), "-T", "3", host], capture_output=True, text=True, timeout=15, check=False)
    return [line for line in result.stdout.splitlines() if line.strip() and not line.startswith("#")]


def _default_key_fingerprint(key_line: str) -> str:
    result = subprocess.run(["/usr/bin/ssh-keygen", "-lf", "-"], input=key_line + "\n", capture_output=True, text=True, timeout=10, check=False)
    parts = result.stdout.split()
    return parts[1] if result.returncode == 0 and len(parts) > 1 and parts[1].startswith("SHA256:") else ""


def pin_host_key(ops: "Ops", ssh: dict[str, Any], known_hosts: Path) -> None:
    """Writes only the scanned key matching the pinned fingerprint; fails before any remote command."""
    host = ssh["target"].rsplit("@", 1)[-1]
    matching = [line for line in ops.keyscan(host, ssh["port"]) if ops.key_fingerprint(line) == ssh["host_fingerprint"]]
    if not matching:
        raise PreflightError("SSH host did not present a key matching the pinned fingerprint")
    _write_private(known_hosts, matching[0] + "\n")


def _default_ssh(ssh: dict[str, Any]) -> Callable[[str], tuple[int, str]]:
    def call(command: str) -> tuple[int, str]:
        result = subprocess.run(
            ["/usr/bin/ssh", "-F", "/dev/null", "-i", ssh["identity_file"], "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes",
             "-o", "StrictHostKeyChecking=yes", "-o", f"UserKnownHostsFile={ssh['known_hosts']}",
             "-o", "ConnectTimeout=5", "-p", str(ssh["port"]), ssh["target"], command],
            capture_output=True, text=True, timeout=20, check=False,
        )
        return result.returncode, result.stdout

    return call


def _default_ws(payload: dict[str, Any]) -> dict[str, Any]:
    script = Path(__file__).with_name("nomad_native_e2e_ws.js")
    result = subprocess.run(["node", str(script)], input=json.dumps(payload), text=True, capture_output=True, timeout=120, check=False)
    if result.returncode != 0:
        raise PreflightError(f"fixture WebSocket driver {payload['mode']} failed")
    try:
        return json.loads(result.stdout.strip().splitlines()[-1])
    except (json.JSONDecodeError, IndexError):
        raise PreflightError("fixture WebSocket driver result is unreadable") from None


@dataclass
class Ops:
    """Every external effect, injectable for offline tests."""
    free_port: Callable[[], int] = _default_free_port
    port_open: Callable[[int], bool] = _default_port_open
    pid_alive: Callable[[int], bool] = _default_pid_alive
    listener_pids: Callable[[int], list[int]] = _default_listener_pids
    registry: Callable[[Path], Any] = _default_registry
    read_db: Callable[[str, Callable[[sqlite3.Connection], Any]], Any] = _default_read_db
    api_factory: Callable[[int], Callable[[str, str, Any], Any]] = _default_api
    nomad_factory: Callable[[str, str, str, str], Callable[[str], Any]] = _default_nomad_get
    keyscan: Callable[[str, int], list[str]] = _default_keyscan
    key_fingerprint: Callable[[str], str] = _default_key_fingerprint
    ssh_factory: Callable[[dict[str, Any]], Callable[[str], tuple[int, str]]] = _default_ssh
    ws: Callable[[dict[str, Any]], dict[str, Any]] = _default_ws
    clock: Callable[[], float] = time.monotonic
    sleep: Callable[[float], Any] = time.sleep
    wall: Callable[[], str] = lambda: datetime.now(timezone.utc).isoformat(timespec="seconds")
    marker_timeout: float = MARKER_TIMEOUT
    allocation_timeout: float = 120.0
    fence_timeout: float = 60.0
    recovery_timeout: float = 120.0


@dataclass
class Evidence:
    root: Path
    secrets: list[str]
    data: dict[str, Any] = field(default_factory=lambda: {"status": "running", "steps": []})

    def scrub(self, text: str) -> str:
        for secret in self.secrets:
            if secret:
                text = text.replace(secret, "[redacted]")
        return text

    def render(self) -> str:
        return self.scrub(json.dumps(self.data, sort_keys=True, indent=2))

    def save(self) -> None:
        _write_private(self.root / EVIDENCE_FILE, self.render() + "\n")


def _write_private(path: Path, text: str) -> None:
    tmp = path.with_name(path.name + ".tmp")
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as handle:
        handle.write(text)
    os.replace(tmp, path)


def _mkdir_private(path: Path) -> Path:
    path.mkdir(mode=0o700)
    path.chmod(0o700)
    return path


def _remote_state_dir(allocation_root: str, node_id: str) -> str:
    return posixpath.join(posixpath.dirname(posixpath.dirname(allocation_root)), "worker-state", node_id)


def _fence_path(state: dict[str, Any]) -> str:
    return posixpath.join(_remote_state_dir(state["allocation_root"], state["node_id"]), "active-environment")


class Runner:
    def __init__(self, root: Path, state: dict[str, Any], token: str, ops: Ops, evidence: Evidence):
        self.root, self.state, self.token, self.ops, self.evidence = root, state, token, ops, evidence
        self.home = root / "home"
        self.registry = ops.registry(root)
        self.api = ops.api_factory(state["port"])
        self.nomad = ops.nomad_factory(state["nomad"]["address"], token, state["nomad"]["namespace"], state["nomad"]["region"])
        known_hosts = root / "known_hosts"
        pin_host_key(ops, state["ssh"], known_hosts)
        self.ssh = ops.ssh_factory({**state["ssh"], "identity_file": str(self.home / ".ssh" / "id_ed25519"), "known_hosts": str(known_hosts)})
        self.database = str(self.home / "data" / "kandev.db")

    # -- bookkeeping -------------------------------------------------------
    def save_state(self, **updates: Any) -> None:
        self.state.update(updates)
        _write_private(self.root / STATE_FILE, json.dumps(self.state, sort_keys=True, indent=2) + "\n")

    def step(self, name: str, fn: Callable[[], Any]) -> Any:
        started, begin = self.ops.wall(), self.ops.clock()
        entry = {"name": name, "started": started, "status": "running"}
        self.evidence.data["steps"].append(entry)
        try:
            result = fn()
        except BaseException:
            entry["status"] = "failed"
            entry["duration_s"] = round(self.ops.clock() - begin, 3)
            raise
        entry["status"] = "passed"
        entry["duration_s"] = round(self.ops.clock() - begin, 3)
        self.evidence.save()
        return result

    def poll(self, timeout: float, interval: float, probe: Callable[[], Any], failure: str) -> Any:
        deadline = self.ops.clock() + timeout
        while True:
            value = probe()
            if value:
                return value
            if self.ops.clock() >= deadline:
                raise PreflightError(failure)
            self.ops.sleep(interval)

    # -- process lifecycle ---------------------------------------------------
    def launch_env(self, barrier: str | None) -> dict[str, str]:
        env = {**self.state["manifest_env"], "KANDEV_BUNDLE_DIR": self.state["bundle"], "PATH": "/usr/bin:/bin:/usr/sbin:/sbin", FIXTURE_ONLY_ENV: "1"}
        if barrier:
            env[BARRIER_ENV] = barrier
        return env

    def start_backend(self, barrier: str | None) -> dict[str, Any]:
        port = self.state["port"]
        if port == bootstrap.LIVE_KANDEV_PORT:
            raise PreflightError("refusing to use the live Kandev port")
        if self.ops.port_open(port):
            raise PreflightError("fixture API port is already in use")
        record = self.registry.start(PROCESS_LABEL, _fixture_argv(self.state["kandev_binary"]), cwd=self.home, env=self.launch_env(barrier))
        self.poll(API_READY_TIMEOUT, 0.25, self._api_ready, "fixture backend did not serve its API before timeout")
        if self.ops.listener_pids(port) != [record["pid"]]:
            raise PreflightError("fixture API listener is not exactly the registered Kandev child")
        return record

    def _api_ready(self) -> bool:
        try:
            self.api("GET", "/workspaces", None)
            return True
        except PreflightError:
            return False

    def verify_gone(self, pid: int) -> None:
        if self.ops.pid_alive(pid):
            raise PreflightError("fixture backend process is still alive")
        if self.ops.port_open(self.state["port"]):
            raise PreflightError("fixture API port is still open")
        if any(item["label"] == PROCESS_LABEL for item in self.registry.read_records()):
            raise PreflightError("fixture backend ownership record was not retired")

    # -- reads -------------------------------------------------------------------
    def inventory(self) -> dict[str, Any]:
        def read(db: sqlite3.Connection) -> list[tuple[Any, ...]]:
            return db.execute("SELECT task_id, runtime, agent_execution_id, metadata FROM executors_running WHERE session_id = ?", (self.state["session_id"],)).fetchall()

        rows = self.ops.read_db(self.database, read)
        if len(rows) != 1:
            raise PreflightError("expected exactly one durable executor inventory row")
        task_id, runtime, execution_id, raw = rows[0]
        try:
            nomad_state = json.loads(raw)["nomad_environment_state"]
        except (TypeError, KeyError, json.JSONDecodeError):
            raise PreflightError("Nomad inventory state is unreadable") from None
        if task_id != self.state["task_id"] or runtime != "nomad":
            raise PreflightError("durable executor inventory row has the wrong owner or runtime")
        snapshot = {key: nomad_state.get(key) for key in _INVENTORY_KEYS}
        snapshot["execution_id"] = execution_id
        snapshot["workspace_input_captured"] = nomad_state.get("workspace_input_captured")
        return snapshot

    def session_view(self) -> tuple[dict[str, Any], list[dict[str, Any]]]:
        sessions = self.api("GET", f"/tasks/{self.state['task_id']}/sessions", None).get("sessions", [])
        current = [item for item in sessions if item.get("id") == self.state["session_id"]]
        if len(current) != 1:
            raise PreflightError("fixture session is not uniquely listed")
        return current[0], self.api("GET", f"/task-sessions/{self.state['session_id']}/messages", None).get("messages", [])

    def nomad_snapshot(self, inventory: dict[str, Any]) -> dict[str, Any]:
        job_id = inventory["job_id"]
        if not isinstance(job_id, str) or not _JOB_ID.fullmatch(job_id):
            raise PreflightError("observed Nomad job id does not follow the kandev-<hash> pattern")
        node = self.nomad(f"/v1/node/{inventory['node_id']}")
        if node.get("ID") != self.state["nomad"]["node_id"] or node.get("Status") != "ready" or node.get("SchedulingEligibility") != "eligible":
            raise PreflightError("Nomad worker identity is not the configured ready and eligible node")
        job = self.nomad(f"/v1/job/{job_id}")
        job = job.get("Job", job)
        allocations = self.nomad(f"/v1/job/{job_id}/allocations")
        if job.get("ID") != job_id or job.get("Status") != "running":
            raise PreflightError("Nomad job is not the running job from the inventory")
        if len(allocations) != 1 or allocations[0].get("ID") != inventory["allocation_id"]:
            raise PreflightError("Nomad job does not have exactly the inventory allocation")
        allocation = self.nomad(f"/v1/allocation/{inventory['allocation_id']}")
        if allocation.get("NodeID") != inventory["node_id"] or allocation.get("ClientStatus") != "running":
            raise PreflightError("Nomad allocation is not running on the inventory node")
        if allocation.get("TaskStates", {}).get("agentctl", {}).get("State") != "running":
            raise PreflightError("Nomad agentctl task is not running")
        return {
            "job_id": job_id, "job_create_index": job.get("CreateIndex"), "job_definition_index": job.get("JobModifyIndex"),
            "allocation_id": allocation["ID"], "node_id": allocation["NodeID"], "allocation_count": len(allocations),
        }

    def remote_tree(self, inventory: dict[str, Any]) -> list[dict[str, Any]]:
        code, out = self.ssh("ps -eww -o pid=,ppid=,pgid=,lstart=,command=")
        if code != 0:
            raise PreflightError("remote process listing failed")
        needles = [inventory["allocation_id"], inventory["job_id"], inventory["environment_id"]]
        rows = []
        for line in out.splitlines():
            match = _PS_ROW.match(line)
            if match and any(needle in match.group(5) for needle in needles):
                rows.append({"pid": int(match.group(1)), "ppid": int(match.group(2)), "pgid": int(match.group(3)), "started": " ".join(match.group(4).split())})
        return sorted(rows, key=lambda row: row["pid"])

    def fence(self) -> str | None:
        """SHA-256 of the worker's active-environment fence, or None when it is absent."""
        path = shlex.quote(_fence_path(self.state))
        code, out = self.ssh(f"if [ -e {path} ]; then cat -- {path}; else exit 3; fi")
        if code == 3:
            return None
        if code != 0:
            raise PreflightError("remote fence read failed")
        return hashlib.sha256(out.encode()).hexdigest()

    # -- create phase ----------------------------------------------------------------
    def setup_api_objects(self, cfg: dict[str, Any]) -> None:
        api = self.api
        workspace = self._first(api("GET", "/workspaces", None).get("workspaces"), "workspace")
        workflow = self._first(api("GET", f"/workspaces/{workspace['id']}/workflows", None).get("workflows"), "workflow")
        step = self._first(api("GET", f"/workflows/{workflow['id']}/workflow/steps", None).get("steps"), "workflow step")
        profiles = [p for agent in api("GET", "/agents", None).get("agents", []) for p in agent.get("profiles", [])]
        wanted = cfg["agent_profile_name"]
        profiles = [p for p in profiles if wanted is None or p.get("name") == wanted]
        if len(profiles) != 1:
            raise PreflightError("expected exactly one matching agent profile on the fresh fixture")
        secret = api("POST", "/secrets", {"name": "nomad-fixture-token", "value": self.token})
        executors = api("GET", "/executors", None).get("executors", [])
        executor = next((item for item in executors if item.get("type") == "nomad"), None) or api("POST", "/executors", {"name": "nomad-fixture", "type": "nomad"})
        nomad = cfg["nomad"]
        source = validate_profile_source({
            "schema_version": 1, "executor_id": executor["id"], "profile_id": str(uuid.uuid4()), "profile_name": "nomad-fixture-profile",
            "nomad_token_ref": f"$kandev-secret:{secret['id']}",
            "config": {
                "address": nomad["address"], "namespace": nomad["namespace"], "region": nomad["region"], "datacenter": nomad["datacenter"],
                "node_id": nomad["node_id"], "platform": nomad["platform"], "ssh_target": cfg["ssh"]["target"], "ssh_port": str(cfg["ssh"]["port"]),
                "ssh_host_fingerprint": cfg["ssh"]["host_fingerprint"], "workspace_root": cfg["workspace_root"],
                "allocation_root": cfg["allocation_root"], "cpu_mhz": str(cfg["cpu_mhz"]), "memory_mb": str(cfg["memory_mb"]),
            },
        })
        profile = api("POST", f"/executors/{executor['id']}/profiles", {**build_profile_payload(source), "mcp_policy": "", "prepare_script": "", "cleanup_script": ""})
        profile = profile.get("profile", profile)
        repository = self._register_repository(cfg, workspace["id"])
        task = api("POST", "/tasks", {
            "workspace_id": workspace["id"], "workflow_id": workflow["id"], "workflow_step_id": step["id"],
            "title": "Nomad pre-ready recovery fixture", "description": "Pre-ready recovery fixture; no prompt is sent.",
            "repositories": [{"repository_id": repository["id"], "base_branch": cfg["repository"]["base_branch"]}],
            "prepare_session": True, "agent_profile_id": profiles[0]["id"], "executor_id": executor["id"], "executor_profile_id": profile["id"],
        })
        if not task.get("id") or not task.get("session_id"):
            raise PreflightError("task creation did not return a task and session")
        self.save_state(workspace_id=workspace["id"], task_id=task["id"], session_id=task["session_id"], repository_id=repository["id"], executor_profile_id=profile["id"])

    def _register_repository(self, cfg: dict[str, Any], workspace_id: str) -> dict[str, Any]:
        repo = cfg["repository"]
        if "path" in repo:
            body = {"name": Path(repo["path"]).name, "source_type": "local", "local_path": repo["path"], "default_branch": repo["base_branch"]}
            return self.api("POST", f"/workspaces/{workspace_id}/repositories", body)
        return self.api("POST", f"/workspaces/{workspace_id}/repositories/remote", {"remote_url": repo["url"], "default_branch": repo["base_branch"]})

    @staticmethod
    def _first(items: Any, label: str) -> dict[str, Any]:
        if not isinstance(items, list) or not items or not isinstance(items[0], dict) or not items[0].get("id"):
            raise PreflightError(f"fresh fixture has no {label}")
        return items[0]

    # -- crash point -------------------------------------------------------------------
    def read_marker(self, marker: Path) -> dict[str, str] | None:
        try:
            text = marker.read_text(encoding="utf-8")
        except FileNotFoundError:
            return None
        lines = [line for line in text.splitlines() if line.strip()]
        if len(lines) != 1:
            return None
        try:
            value = json.loads(lines[0])
        except json.JSONDecodeError:
            return None
        return value if isinstance(value, dict) else None

    def await_barrier(self, marker: Path) -> dict[str, Any]:
        found = self.poll(self.ops.marker_timeout, 0.25, lambda: self.read_marker(marker), "pre-publish barrier marker did not appear before timeout")
        if found.keys() != MARKER_KEYS or any(not isinstance(found[key], str) or not found[key] for key in MARKER_KEYS):
            raise PreflightError("barrier marker does not contain exactly execution_id, environment_id, session_id")
        if found["session_id"] != self.state["session_id"]:
            raise PreflightError("barrier marker belongs to a different session")
        inventory = self.inventory()
        if inventory["execution_id"] != found["execution_id"] or inventory["operation_id"] != found["execution_id"] or inventory["environment_id"] != found["environment_id"]:
            raise PreflightError("durable inventory does not match the barrier marker identities")
        if inventory["phase"] != "ready" or inventory["workspace_input_captured"] is not True:
            raise PreflightError("durable inventory is not at phase ready with its workspace input captured")
        self.save_state(environment_id=found["environment_id"], execution_id=found["execution_id"], job_id=inventory["job_id"], allocation_id=inventory["allocation_id"], node_id=inventory["node_id"])
        return inventory

    def check_environment_creating(self, environment_id: str) -> int:
        return self.ops.read_db(self.database, lambda db: base.check_pre_ready_environment(db, environment_id))

    def check_session_created(self) -> list[dict[str, Any]]:
        row, messages = self.session_view()
        base.check_pre_ready_session(row, messages)
        return messages

    # -- teardown ------------------------------------------------------------------------
    def teardown(self) -> dict[str, Any]:
        """Production stop path, then verification; fails on the first unmet condition."""
        report: dict[str, Any] = {}
        if not any(item["label"] == PROCESS_LABEL for item in self.registry.read_records()):
            self.start_backend(None)
        record = next(item for item in self.registry.read_records() if item["label"] == PROCESS_LABEL)
        if self.state.get("session_id"):
            self.ops.ws({"mode": "session-stop", "ws_base": f"ws://127.0.0.1:{self.state['port']}", "session_id": self.state["session_id"]})
        if self.state.get("allocation_id"):
            allocation_id = self.state["allocation_id"]

            def terminal() -> str | None:
                status = self.nomad(f"/v1/allocation/{allocation_id}").get("ClientStatus")
                return status if status in TERMINAL_ALLOCATION_STATES else None

            report["allocation_state"] = self.poll(self.ops.allocation_timeout, 1.0, terminal, "Nomad allocation did not reach a terminal state after the session stop")
            self.poll(self.ops.fence_timeout, 1.0, lambda: self.fence() is None, "worker active-environment fence is still present after the session stop")
            report["fence_released"] = True
        if self.registry.stop(PROCESS_LABEL, timeout=15) not in ("stopped", "already_absent"):
            raise PreflightError("fixture backend did not stop through its exact owned process record")
        self.verify_gone(record["pid"])
        report["backend_pid_stopped"] = record["pid"]
        if self.state.get("allocation_id"):
            inventory = {key: self.state[key] for key in ("allocation_id", "job_id", "environment_id")}
            if self.remote_tree(inventory):
                raise PreflightError("remote runtime processes are still present after teardown")
            report["remote_processes_remaining"] = 0
        return report


def _new_state(cfg: dict[str, Any], root: Path, port: int, token_file: str) -> dict[str, Any]:
    home = root / "home"
    env = {
        "HOME": str(home), "KANDEV_HOME_DIR": str(home), "KANDEV_DATABASE_PATH": str(home / "data" / "kandev.db"),
        "KANDEV_SERVER_PORT": str(port),
    }
    return {
        "schema_version": 1, "port": port, "kandev_binary": cfg["kandev_binary"], "bundle": cfg["bundle"], "manifest_env": env,
        "allocation_root": cfg["allocation_root"], "node_id": cfg["nomad"]["node_id"],
        "nomad": {"address": cfg["nomad"]["address"], "token_file": token_file, "namespace": cfg["nomad"]["namespace"], "region": cfg["nomad"]["region"], "node_id": cfg["nomad"]["node_id"]},
        "ssh": {"target": cfg["ssh"]["target"], "port": cfg["ssh"]["port"], "host_fingerprint": cfg["ssh"]["host_fingerprint"]},
        "expected_file": cfg["expected_file"], "expected_repo_count": 1,
    }


def _materialize(cfg: dict[str, Any], root: Path, port: int) -> None:
    home = _mkdir_private(root / "home")
    _mkdir_private(home / ".ssh")
    _mkdir_private(home / "data")
    _mkdir_private(home / "supervisor")
    identity = home / ".ssh" / "id_ed25519"
    fd = os.open(identity, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "wb") as handle:
        handle.write(Path(cfg["ssh"]["identity_file"]).read_bytes())
    env = {"HOME": str(home), "KANDEV_HOME_DIR": str(home), "KANDEV_DATABASE_PATH": str(home / "data" / "kandev.db"), "KANDEV_SERVER_PORT": str(port)}
    manifest = {"version": 1, "home_dir": str(home), "backend_executable": cfg["kandev_binary"], "argv": ["__backend"], "port": port, "env": env}
    _write_private(home / "supervisor" / "launch.json", json.dumps(manifest, sort_keys=True))
    base._verify_backend_launch_manifest(root, home, {"binary": cfg["kandev_binary"], "port": port})


def run_create_fixture(config: Any, ops: Ops | None = None, out: Callable[[str], Any] = print, err: Callable[[str], Any] | None = None,
                       run: Callable[..., Any] = subprocess.run, checkout_root: Path = bootstrap.CHECKOUT_ROOT) -> int:
    ops = ops or Ops()
    err = err or (lambda text: print(text, file=sys.stderr))
    try:
        cfg = bootstrap.validate_bootstrap(config, run=run, checkout_root=checkout_root)
        token = bootstrap.read_token(Path(cfg["nomad"]["token_file"]))
        port = ops.free_port()
        if port == bootstrap.LIVE_KANDEV_PORT:
            raise PreflightError("refusing to use the live Kandev port")
    except (PreflightError, OSError) as exc:
        err(f"real pre-ready recovery bootstrap failed: {exc}")
        return 2
    root = Path(tempfile.mkdtemp(prefix="nomad-e2e-fixture-", dir=cfg["temp_root_parent"]))
    root.chmod(0o700)
    evidence = Evidence(root, [token])
    evidence.data.update({"mode": "created-real-pre-ready-recovery", "fixture_root": str(root), "port": port, "started": ops.wall()})
    runner: Runner | None = None
    try:
        _materialize(cfg, root, port)
        state = _new_state(cfg, root, port, cfg["nomad"]["token_file"])
        runner = Runner(root, state, token, ops, evidence)
        runner.save_state()
        evidence.save()
        report = _drive(runner, cfg, ops, root)
    except Exception as exc:
        steps = evidence.data["steps"]
        failed = next((item["name"] for item in reversed(steps) if item["status"] == "failed"), steps[-1]["name"] if steps else "materialize")
        evidence.data.update({"status": "failed", "failed_step": failed, "error": evidence.scrub(str(exc)), "finished": ops.wall()})
        evidence.save()
        err(evidence.scrub(f"real pre-ready recovery failed: {exc}"))
        out(evidence.render())
        out(f"evidence: {root / EVIDENCE_FILE}")
        out(f"operator cleanup: {cleanup_command(root)}")
        return 2
    evidence.data.update({"status": "passed", "finished": ops.wall(), **report})
    evidence.save()
    out(f"evidence: {root / EVIDENCE_FILE}")
    out(evidence.render())
    return 0


def _drive(runner: Runner, cfg: dict[str, Any], ops: Ops, root: Path) -> dict[str, Any]:
    step, state, data = runner.step, runner.state, runner.evidence.data
    marker = root / "barrier-marker.json"
    first = step("start-backend-with-barrier", lambda: runner.start_backend(str(marker)))
    step("create-fixture-objects", lambda: runner.setup_api_objects(cfg))
    inventory = step("await-pre-publish-barrier", lambda: runner.await_barrier(marker))
    environment_id = inventory["environment_id"]

    def pre_crash() -> dict[str, Any]:
        generation = runner.check_environment_creating(environment_id)
        messages = runner.check_session_created()
        return {"generation": generation, "messages": messages, "nomad": runner.nomad_snapshot(inventory), "tree": runner.remote_tree(inventory), "fence": runner.fence()}

    before = step("snapshot-before-crash", pre_crash)
    if not before["tree"] or before["fence"] is None:
        raise PreflightError("remote runtime process tree or worker fence is missing before the crash")
    if before["generation"] != inventory["generation"]:
        raise PreflightError("environment ownership generation differs from the inventory generation")

    def crash() -> None:
        if runner.registry.crash(PROCESS_LABEL, timeout=10) != "crashed":
            raise PreflightError("fixture backend was not crashed through its exact owned process record")
        runner.verify_gone(first["pid"])

    step("crash-backend", crash)
    second = step("restart-backend-without-barrier", lambda: runner.start_backend(None))
    if second["pid"] == first["pid"]:
        raise PreflightError("restarted backend reused the crashed process identity")

    def recovered() -> dict[str, Any]:
        inventory_after = base._await_recovered_environment(
            lambda fn: ops.read_db(runner.database, fn), runner.inventory, environment_id=environment_id,
            generation_before=before["generation"], expected_repo_count=state["expected_repo_count"],
            timeout=ops.recovery_timeout, clock=ops.clock, sleep=ops.sleep,
        )
        row, messages = runner.session_view()
        base._check_session_untouched(row, messages, before["messages"])
        return {"inventory": inventory_after, "nomad": runner.nomad_snapshot(inventory_after), "tree": runner.remote_tree(inventory_after), "fence": runner.fence()}

    after = step("await-recovery", recovered)

    def identity() -> None:
        if after["inventory"] != inventory:
            raise PreflightError("recovery changed the execution, job, allocation, or server identity")
        if after["nomad"] != before["nomad"]:
            raise PreflightError("recovery changed the Nomad job or allocation identity")
        if after["fence"] != before["fence"]:
            raise PreflightError("recovery changed the worker fence")
        if after["tree"] != before["tree"]:
            raise PreflightError("recovery did not preserve the remote runtime process tree")

    step("assert-identity-preserved", identity)

    def workspace_read() -> None:
        read = ops.ws({"mode": "workspace-read", "ws_base": f"ws://127.0.0.1:{state['port']}", "session_id": state["session_id"], "task_id": state["task_id"], "expected_file": state["expected_file"]})
        base._check_workspace_read(read)
        runner.check_session_created()

    step("workspace-read-before-any-prompt", workspace_read)
    cleanup = step("cleanup-production-stop-path", runner.teardown)
    return {
        "task_id": state["task_id"], "session_id": state["session_id"], "environment_id": environment_id, "execution_id": inventory["execution_id"],
        "job_id": inventory["job_id"], "allocation_id": inventory["allocation_id"], "node_id": inventory["node_id"],
        "server_identity": inventory["server_identity"], "backend_pids": {"before_crash": first["pid"], "after_restart": second["pid"]},
        "environment_status": {"before": "creating", "after": "ready"}, "inventory_phase": {"before": inventory["phase"], "after": after["inventory"]["phase"]},
        "workspace_path": after["inventory"]["workspace_path"], "fence_sha256": before["fence"], "prompts_sent": 0, "cleanup": cleanup,
    }


def create_fixture_command(config_path: str) -> int:
    try:
        config = json.loads(Path(config_path).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        print("real pre-ready recovery bootstrap failed: config is unreadable", file=sys.stderr)
        return 2
    return run_create_fixture(config)


def load_fixture_state(root_text: str) -> tuple[Path, dict[str, Any]]:
    root = base._owned_private_root(root_text)
    path = root / STATE_FILE
    info = path.lstat() if path.exists() else None
    if info is None or not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise PreflightError("fixture state file is missing or not private")
    state = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(state, dict) or state.get("schema_version") != 1 or state.get("port") == bootstrap.LIVE_KANDEV_PORT:
        raise PreflightError("fixture state is invalid")
    return root, state


def run_cleanup_fixture(root_text: str, ops: Ops | None = None, out: Callable[[str], Any] = print, err: Callable[[str], Any] | None = None) -> int:
    ops = ops or Ops()
    err = err or (lambda text: print(text, file=sys.stderr))
    try:
        root, state = load_fixture_state(root_text)
        token = bootstrap.read_token(Path(state["nomad"]["token_file"]))
    except (PreflightError, OSError, json.JSONDecodeError, KeyError) as exc:
        err(f"cleanup-fixture failed before any action: {exc}")
        return 2
    evidence = Evidence(root, [token])
    try:
        evidence.data = json.loads((root / EVIDENCE_FILE).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        evidence.data = {"status": "unknown", "steps": []}
    try:
        runner = Runner(root, state, token, ops, evidence)
        report = runner.step("cleanup-fixture", runner.teardown)
    except Exception as exc:
        evidence.data["cleanup"] = {"status": "failed", "error": evidence.scrub(str(exc)), "finished": ops.wall()}
        evidence.save()
        err(evidence.scrub(f"cleanup-fixture failed: {exc}"))
        out(f"evidence: {root / EVIDENCE_FILE}")
        out(f"operator cleanup: {cleanup_command(root)}")
        return 2
    evidence.data["cleanup"] = {"status": "passed", "finished": ops.wall(), **report}
    evidence.save()
    out(f"evidence: {root / EVIDENCE_FILE}")
    out(evidence.render())
    return 0


def cleanup_fixture_command(root_text: str) -> int:
    return run_cleanup_fixture(root_text)
