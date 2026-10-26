---
status: draft
system: agents
created: 2026-10-26
requirements:
  - REQ-AGENTS-SANDBOX-012
---

# Sandboxed session system design

Extends [Sandbox policy system design](sandbox-policies.md), which owns policy, admission, attempts and reports. This file owns the end-to-end session contract, the launch-state summary, and the conformance suite that gates release. Ownership of work orders and the order of delivery are in the [plan](../../../plans/nono-sandbox-integration/plan.md).

## Requirement-to-mechanism map

| AC | Mechanism | Work order |
| --- | --- | --- |
| 012.1 | `newSandboxRuntime` returns a typed reason; the backend installs a refusing runtime instead of exiting; availability gains `reason_code` | 19 |
| 012.2 | Agentctl maps exit status and a stderr tail to a closed cause using core-supplied hints; only the cause leaves agentctl | 19, 15 |
| 012.3 | Pinned attempt revision (existing); admission re-run on replacement | 04, 19 |
| 012.4 | Guarded MCP surface; canary only | 11, 23 |
| 012.5 | Per-session MCP token and broker relay | 12, 21, 23 |
| 012.6 | Route enumeration test plus fixed exemption list on the existing `instanceAuth` | 17 |
| 012.7 | Managed Git isolation; canary only | 13, 21, 23 |
| 012.8 | One start entry taking `SandboxProcessKind`; labels in the snapshot | 18 |
| 012.9 | Per-task preview origin | 16 |
| 012.10 | Existing `EnvironmentKeys` enforcement; name-only reporting | 04, 19 |
| 012.11 | Credential registry consulted at admission | 15 |
| 012.12 | Executor-type capability table | 19 |
| 012.13 | Conformance suite and canary set | 20, 23 |
| 012.14 | Attempt-derived summary in the execution snapshot and Execution details | 19, 10 |
| 012.15 | Guard ceiling check inside the child-insert transaction | 22 |
| 012.16 | Bind reported in `/identity`, checked in `prepareExecution`; launcher default | 17 |

## Session summary state

The execution snapshot (Task 10) gains `sandbox_summary`: `confined`, `degraded` or `refused`, plus `items`, a list of `{ac, state, reason}`. State is one of `holds`, `degraded`, `unsupported`, `not_applicable`, `unknown`. `unsupported` means the capability is withheld and nothing is exposed (a withheld preview); it does not prevent `confined`. `degraded` and `unknown` do. A session with a setup script labelled `unsandboxed` is therefore `degraded` in the first delivery, which is the intended honest result.

| Item | Applies | `holds` when | Otherwise |
| --- | --- | --- | --- |
| 012.1 | always | runtime available at admission | refused |
| 012.2 | always | no pre-handshake failure (`not_applicable` after a clean handshake) | `degraded` with the cause code |
| 012.3 | always | revision pinned | `unknown` if pin evidence missing |
| 012.4 | guarded session | server enforcement evidence present | `unknown` |
| 012.5 | always | token active for the attempt | `unknown` if instance does not report token support |
| 012.6 | always | instance advertises `rest_auth` in `/identity` | `unknown` |
| 012.7 | always | managed Git isolation active | `degraded` `repo_opted_in` |
| 012.8 | always | every Kandev-started process so far is sandboxed | `degraded` `process_unsandboxed` |
| 012.9 | always | separate origin serving | `unsupported` `preview_origin_unavailable` |
| 012.10 | always | environment filtered | `unknown` |
| 012.11 | always | a registry method satisfied | refused |
| 012.12 | always | executor type supported | refused |
| 012.15 | child tasks | within ceiling | refused at creation (no attempt) |
| 012.16 | always | instance bind loopback | refused; `degraded` `feature_disabled` when the flag is off |

The reason set is closed: the codes named in the requirement plus `feature_disabled`, `repo_opted_in`, `process_unsandboxed`, `evidence_unavailable`. The summary is computed at admission and recomputed only by attempt events: `attempt_admitted`, `process_started` (label), `preview_state_changed`, `exit_diagnosed`, `token_revoked`, `evidence_lost`, `attempt_ended`. After `attempt_ended` it is frozen. The web client renders from the snapshot only and derives no state; on WebSocket loss it hides the shield and shows "status unavailable", as the inherited projection already does. On the desktop tab and the phone control the shield follows `confined` only; `degraded` shows a warning indicator; `refused` shows a failed indicator. Every reason code has localized text and a remedy in seven locales.

## Launch admission order

`sandboxLaunchAdmission.admit` resolves policy, then runs runtime availability (012.1) and the credential registry (012.11). `prepareExecution`, where the executor type and the instance `/identity` are known, then runs the executor capability table with the AC-AGENTS-SANDBOX-011.2 host test (012.12) and the bind check (012.16), all before runtime preparation. The first refusal is returned; items after it are recorded `unknown`. With the summary flag on, a refusal writes an attempt in state `refused` (no compile, no process, no root) holding the evaluated items; Task 10's snapshot reads it. If that write fails the refusal is still returned. The existing recovery path (Task 02) retries a refused launch as a new launch.

## Credential registry (012.11)

A table in `internal/agent/sandboxruntime` keyed by agent type and host platform, holding a list of methods `{kind, delivery}` and a list of `missing_markers` (strings used by exit diagnosis). `env_key` names the variable the policy must allowlist; `file_in_agent_state` names a path relative to the `agent_state` grant; `host_keychain` is never satisfied. Registry data holds names and paths only and is the single source for the admission check and the setup hint. Initial entries, provisional until measured on a real host and recorded in `acceptance.md`: `claude-acp` (`env_key` `ANTHROPIC_API_KEY`; `file_in_agent_state` for its credentials file; `host_keychain` on darwin) and `codex-acp` (`env_key` `OPENAI_API_KEY`; `file_in_agent_state` for its auth file). Any other agent is `sandbox_credentials_unknown`.

## Agent exit diagnosis (012.2)

Core computes at admission and passes to agentctl with the start request: `executable_readable` (the agent's resolved executable and its install root are readable under the pinned grants) and the agent's `missing_markers`. Agentctl keeps the last 2 KiB of the child's stderr, redacts it with the existing log redaction, and on a pre-handshake exit or an ACP initialize timeout chooses: `agent_binary_denied` if `executable_readable` is false; else `agent_credentials_missing` if the redacted tail contains a marker; else `sandbox_denied_other` if it contains one of the fixed denial tokens kept in agentctl (`Operation not permitted`, `Permission denied`, `EPERM`, `EACCES`); else `agent_handshake_timeout` for a timeout with no exit; else `agent_exited`. Empty stderr, signal death and a missing exit status all give `agent_exited`. Agentctl discards the tail and sends core only the cause, which core persists as `launch_cause` and which the session error card and Execution details render. No provider marker contract is used.

## REST auth and bind (012.6, 012.16)

Agentctl already authenticates routes with `instanceAuth` (`agentctl/server/api/server.go`) and already binds by `AGENTCTL_LISTEN_HOST` / `agent.standaloneHost` (default `localhost`). The delta is: a test that enumerates registered routes against the fixed exemption list; identical 401 bodies and constant-time comparison for every bad-credential case; `/identity` fields `bind` (from the real listener, not the config) and `rest_auth`; the launcher defaulting sandboxed Local and Worktree instances to loopback when the flag is on, with an explicit `agent.standaloneHost` taking precedence; the check in `prepareExecution` refusing `sandbox_bind_exposed`; and the code-server process (`agentctl/server/process/vscode.go`, currently `0.0.0.0`) binding loopback for a sandboxed attempt. `server.host` is the backend's own key and is untouched.

## Process surface (012.8)

A new type `SandboxProcessKind` in `internal/agent/sandbox` with the closed values of the requirement. It maps from the existing `streams.ProcessKind` (`setup`, `cleanup`, `custom`, `dev`) and adds `language_server`, `terminal`, `code_server`, `helper`; `agent_passthrough` is the agent itself and is excluded. Every Kandev process start in lifecycle, scriptengine and agentctl process management passes through one entry that takes the kind and the attempt, writes the label through the snapshot store, and only then starts the process; output events carry the label id. A write failure aborts the start with `process_label_unrecorded`. A process shared by several attempts of one task (dev server, language server) is labelled by the attempt that started it and shown with that label to the others. Setup and cleanup scripts are `unsandboxed` in the first delivery. Terminals reuse the existing refusal on darwin. A test using the Go AST fails when a process start appears outside the entry and an allowlist.

## Preview origin (012.9)

Covers the HTML preview and the session port proxy; the browser-facing proxy is `gateway/websocket/port_proxy.go`, with `agentctl/server/api/workspace_preview.go` as the publish handler. The proxy answers on `<task-token>.<preview.baseDomain>`, a new config key; the token is random, per task, stable for the task, and invalid after task deletion. `preview.baseDomain` must differ from the UI host and must not be its parent domain, and UI cookies must be host-only; both are asserted by tests. Redirects keep the preview origin, WebSockets use the same routing, and requests keep the existing capability check. With no base domain configured (desktop, loopback installs) or the flag off, sandboxed previews are disabled with `preview_origin_unavailable` and the UI hides the control and shows the reason. This amends ADR-2026-09-05 for sandboxed sessions only; see the new ADR `2026-10-26-sandboxed-preview-origin`.

## Child ceiling (012.15)

Task creation reads the parent's task guard and inserts the child in one transaction, comparing requested grants to the guard by role and access as the requirement states. `sandbox_outside_ceiling` is a new typed error. Today the guard is copied at creation and a wider child is rejected only later at launch by `checkParentGuard` (`agent/sandbox/task_admission.go`); that launch check stays as defense in depth.

## Conformance suite (012.13)

**Shape.** A Go test binary `cmd/sandbox-conformance` plus a shell wrapper. It drives an isolated Kandev instance (temporary data directory under the user home, OS-assigned port, never the default instance) through the REST API with a real agent profile, and records JSON results. It supersedes the vocabulary of the AC-AGENTS-SANDBOX-011.11 darwin suite: that suite stays as a lower-level harness whose canaries the new suite wraps and maps (`allowed`/`denied` to the control/probe pair, `control_failed` to `invalid`, `skipped` to `not_applicable`).

**Journey.** Scripted, in order: enable flag, install provider package, create an org-scoped policy, bind to the executor, check availability, fault step (data directory under a temporary root, expect `state_root_denied` and a running backend), fault step (agent binary outside the grant, expect `agent_binary_denied`), create a task with the agent, send a prompt and read the reply, open a native terminal (expect `sandbox_transport_unsupported` on macOS), call a Kandev MCP tool, run `git commit` and `git push` to a local bare fixture remote through the broker, trigger a denial and read it in the snapshot and report, request and answer an approval, restart the backend mid-session and resume, cancel, clean up. Each step is `passed`, `failed` or `not_applicable`. Every step has a 120 s bound and the journey 20 min; a poll never retries a failed assertion. Restart asserts the resumed attempt keeps the same policy revision and the same summary; cancel asserts the task state and that no owned process remains; cleanup asserts the attempt root and the instance data directory are gone. Step `not_applicable` reasons come from the closed list: `host_os`, `approvals_unsupported_host`, `transport_unsupported`, `git_identity_excluded`, `git_common_dir_excluded`, `executor_not_in_cell`, `flag_dependency`. On macOS the commit and push step is `git_identity_excluded` (Local) or `git_common_dir_excluded` (Worktree); the broker relay is therefore verified on Linux only in this delivery.

**Canaries.** Each has a probe and a control. A sandbox control runs unsandboxed in the same fixture and must succeed. A server control repeats the probe request with the valid credential and must succeed, proving the route is reachable and the probe's denial is due to the guard. Results are `contained`, `escaped`, `invalid`, `not_applicable` (reason from the list above) or `error` (timeout, crash, absent target); only `contained` with a valid control passes. Fixture names are generated markers; only marker ids are printed.

| Canary | AC | Control |
| --- | --- | --- |
| `G1_child_widen`, `G1_child_concurrent` | 012.15 | server: in-ceiling child created |
| `G1_mcp_unguarded_tool` | 012.4 | server: same tool from an unguarded session |
| `G1_pin_policy_change` | 012.3 | sandbox: a new attempt picks the new revision |
| `G2_no_token`, `G2_wrong_token`, `G2_finished_token` | 012.5 | server: valid token |
| `G2_rest_no_credential` | 012.6 | server: valid credential |
| `G2_nonloopback_bind` | 012.16 | server: loopback instance admitted |
| `G3_repo_hook`, `G3_repo_fsmonitor`, `G3_repo_filter` | 012.7 | opt-in repository runs the code |
| `G3_process_label_<kind>` per kind | 012.8 | a test-only unlabelled start is flagged by the checker |
| `G4_preview_cookie_read`, `G4_preview_storage_read` | 012.9 | a page on the UI origin reads the marker |
| `G5_env_not_allowlisted`, `G5_env_absent_not_empty` | 012.10 | unsandboxed child sees the marker variable |
| `G6_keychain_read` (darwin) | 012.11 | unsandboxed read of a fixture keychain item |
| `G6_credential_file_outside_state` | 012.11 | unsandboxed read of the file |
| `G6_launch_refused_missing` | 012.11 | launch with the method satisfied is admitted |
| `G7_executor_unsupported` | 012.12 | Local launch admitted |

**Cells and agents.** A cell is the tuple agent, host OS, executor (Local or Worktree): eight cells. A cell passes only when every journey step is `passed` or `not_applicable`, every canary is `contained` or `not_applicable`, and none is `invalid` or `error`.

**Where it runs.** A self-hosted runner per host OS, in a workflow triggered manually and on a schedule, never on forks, with a concurrency group per runner so runs never overlap. A trap removes the isolated instance and its data directory on cancel. Agent credentials come from the runner's secret store and reach the agent only through the policy's environment keys or the provisioned agent state; the workflow never echoes them.

**Result record.** Each run writes a JSON artifact (suite version, commit, `scope_hash`, host OS, agent type, executor, step and canary results, counts) containing no path outside the fixture root and no environment value. `sandbox-conformance --acceptance-line <artifact>` prints one line with the artifact digest, and a maintainer appends it to `acceptance.md` in a pull request; CI does not commit. `scope_hash` is the SHA-256 of the files listed in `docs/plans/nono-sandbox-integration/conformance-scope.txt` (the paths implementing 012.1 to 012.16). `sandbox-conformance --verify-release <sha>` fails when a required cell has no `full` result with the current `scope_hash` and a clean tree. A run on a dirty tree or without a real agent is `partial` and satisfies nothing.

## Rollout

| Flag | Default | Restart | Purpose |
| --- | --- | --- | --- |
| `features.sandboxPolicies` | off in prod, dev and e2e | yes | enables providers, policies, operator routes |
| `features.sandboxSessionSummary` | off | no | summary, refused record, new refusals and reason codes, labels, exit causes |
| `features.agentctlLoopbackBind` | off | yes | loopback default for sandboxed instances and its enforcement |
| `features.previewSeparateOrigin` | off | yes | per-task preview origins |

The three new flags are registered through the runtime flag registry with the profile and frontend contract tests. The two dependent flags are consulted only while the summary flag is on (see the requirement). Promotion order: summary, then loopback bind, then preview origin, each turned on in `dev` first. `features.sandboxPolicies` moves to `prod: true` only when, for the commit being released, `--verify-release` passes for the six required cells (Claude Code and Codex on Linux, and Claude Code on macOS, each on Local and Worktree), with no `invalid` or `error` canary, and the three new flags are on in `prod`. A change to any file in `conformance-scope.txt` changes `scope_hash` and invalidates earlier results. While any cell is missing the feature stays experimental with the risk text unchanged.

## Failure handling summary

A refusal is a launch outcome, not a crash. Registry, bind and diagnosis failures degrade to the most restrictive result: unknown credential refuses, unreported bind refuses, unknown host capability is unsupported. No check falls back to allowing a launch.
