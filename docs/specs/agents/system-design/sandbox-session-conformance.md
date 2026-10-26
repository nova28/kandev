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
| 012.3 | Pinned attempt revision (existing); admission re-run on new-process resume, recovery and replacement | 04, 19 |
| 012.4 | Guarded MCP surface; canary only | 11, 23 |
| 012.5 | Per-instance MCP token and broker relay (REQ-EXECUTORS-SESSION-CONTROL-ISOLATION-001/002) | 12, 21, 23 |
| 012.6 | Route enumeration test, fixed exemption list on the existing `instanceAuth`, `rest_auth` check at admission | 17A, 19 |
| 012.7 | Managed Git isolation; canary only | 13, 21, 23 |
| 012.8 | One start entry taking `SandboxProcessKind`; labels in the snapshot | 18 |
| 012.9 | Per-task preview origin and ticket | 16 |
| 012.10 | Existing `EnvironmentKeys` enforcement; name-only reporting | 04, 19 |
| 012.11 | Credential registry consulted at admission | 15 |
| 012.12 | Executor-type capability table | 19 |
| 012.13 | Conformance suite and canary set | 20, 23 |
| 012.14 | Attempt-derived summary in the execution snapshot and Execution details | 24, 19, 10 |
| 012.15 | Guard ceiling check inside the child-insert transaction | 22 |
| 012.16 | Bind reported in `/identity`, checked in `prepareExecution`; launcher default | 17A, 19 |

## Session summary state

The execution snapshot (Task 10) gains `sandbox_summary`: `confined`, `degraded` or `refused`, plus `items`, a list of `{ac, state, reason}`. The item set is fixed, one item per AC in the table, never duplicated; clients ignore ids they do not know. State is one of `holds`, `degraded`, `unsupported`, `not_applicable`, `unknown`, `refused`. `unsupported` means the capability is withheld and nothing is exposed (a withheld preview); it does not prevent `confined`. `degraded` and `unknown` do. A session with a setup script labelled `unsandboxed` is therefore `degraded` in the first delivery, which is the intended honest result.

`reason` is required for every state except `holds` (where an informational reason such as `repo_opted_in` is allowed). `not_applicable` always carries one of `no_guard`, `not_child`, `handshake_pending`, `clean_handshake`.

A refusing item shows `refused` with its code; items evaluated before it show `holds`; later and runtime items show `unknown` with reason `not_evaluated`.

| Item | Applies | `holds` when | Otherwise |
| --- | --- | --- | --- |
| 012.1 | always | runtime available at admission | `refused` |
| 012.2 | always | never `holds`: `not_applicable` `handshake_pending`, then `clean_handshake` | `degraded` with the cause code |
| 012.3 | always | revision pinned | `unknown` `evidence_unavailable` |
| 012.4 | pinned attempt carries a task guard | `guard_enforced` flag recorded at admission | `unknown` `evidence_unavailable`; `not_applicable` `no_guard` |
| 012.5 | always | token active for the attempt; revocation at attempt end keeps `holds` | `degraded` `token_revoked` on an unexpected mid-attempt revocation; `unknown` if the instance reports no token support |
| 012.6 | always | `/identity` `rest_auth` is `enforced` | `refused` `sandbox_rest_auth_unenforced` |
| 012.7 | always | managed Git isolation active; `holds` with `repo_opted_in` when the repo enabled hooks | `unknown` `evidence_unavailable` |
| 012.8 | always | every Kandev-started process so far is sandboxed or a `helper` | `degraded` `process_unsandboxed` |
| 012.9 | always | separate origin serving | `unsupported` `preview_origin_unavailable` |
| 012.10 | always | environment report present | `unknown` `evidence_unavailable` |
| 012.11 | always | a registry method satisfied | `refused` |
| 012.12 | always | executor type supported | `refused` |
| 012.15 | child tasks | appears only as a refusal error at child creation (no attempt, no summary); child attempts show `holds` | `not_applicable` `not_child` |
| 012.16 | always | instance bind loopback | `refused`; `degraded` `feature_disabled` when the flag is off |

The reason set is closed: the codes named in the requirement plus `feature_disabled`, `repo_opted_in`, `process_unsandboxed`, `evidence_unavailable`, `not_evaluated`, `token_revoked`, `no_guard`, `not_child`, `handshake_pending`, `clean_handshake`, `host_os`.

**Events.** The summary is computed at admission and recomputed only by these events: `attempt_admitted`, `process_started`, `preview_state_changed`, `exit_diagnosed`, `token_revoked`, `git_policy_changed` (the hooks setting changed, recomputes 012.7), `evidence_lost`, `attempt_ended`. After `attempt_ended` it is frozen. `evidence_lost` fires when the agentctl instance is unreachable for more than 30 s or the provider evidence stream ends before `attempt_ended`; runtime items become `unknown` `evidence_unavailable` until evidence returns.

**Indicator matrix** (first match wins; the web client renders from the snapshot and derives no state):

| Status / summary | Indicator |
| --- | --- |
| summary `refused`, or status `failed` | failed |
| status `active`, summary `degraded` | warning |
| status `active`, summary `confined` | shield |
| anything else (including sessions without a summary) | the existing 008.3 rules |

On WebSocket loss the client hides the shield and shows "status unavailable", as the inherited projection does. Every reason code has localized text and a remedy in seven locales.

## Launch refusal record

A table `sandbox_launch_refusals` (`session_id`, `refusal_id`, `items`, `summary`, `refusal_code`, `created_at`) separate from `sandbox_attempts`. It is written only while `features.sandboxSessionSummary` is on, one row per refused launch request, and pruned to the latest 20 per session on write (order `created_at` desc, then `refusal_id` desc). No attempt reader sees it. If policy resolution fails every item is `unknown` `not_evaluated` and the refusal code is the policy error class. The current attempt shown is the newer of `LatestSessionAttempt` and the latest refusal by creation time then id. If the write fails the launch error code still reaches the client, which shows the failed indicator and "details unavailable" plus the code in Execution details.

## Launch admission order

`sandboxLaunchAdmission.admit` resolves policy, then runs runtime availability (012.1) and the credential registry (012.11). `prepareExecution`, where the executor type and the instance `/identity` are known, then runs the executor capability table with the AC-AGENTS-SANDBOX-011.2 host test (012.12), the bind check (012.16), then the `rest_auth` check (012.6), all before runtime preparation. The order is exactly the requirement's evaluation order. The checks are an ordered registry in Task 24 that Tasks 15, 17A and 19 plug into. The first refusal is returned and written to the refusal record; later items are `unknown` `not_evaluated`. Continuation on a live attempt runs none of them. A new-process resume, recovery or replacement re-runs them, keeps the pinned revision, writes a new attempt with `restart_of`, and on refusal keeps the conversation and writes a refusal record.

## Credential registry (012.11)

A table in `internal/agent/sandboxruntime` keyed by agent type and host platform, holding a list of methods `{kind, delivery}` (registry order, deduplicated) and a list of `missing_markers`. Kinds: `env_key` (names the variable the policy must allowlist), `file_in_agent_state` (a path relative to the `agent_state` grant), `host_keychain` (never satisfied), `none`. An entry with an empty method list is invalid and treated as unknown; a build test rejects it. The remedy shows the env key name and the relative path. Registry data holds names and paths only and is the single source for the admission check and the setup hint. Initial entries, provisional until measured on a real host and recorded in `acceptance.md`: `claude-acp` (`env_key` `ANTHROPIC_API_KEY`; `file_in_agent_state` for its credentials file; `host_keychain` on darwin), `codex-acp` (`env_key` `OPENAI_API_KEY`; `file_in_agent_state` for its auth file), and the e2e mock agent (`none`). Any other agent is `sandbox_credentials_unknown`, intended and listed in Out of scope.

## Child environment (012.10)

The child environment is the policy allowlist, plus the provider-declared minimal set, plus a Kandev-injected set: `GIT_CONFIG_NOSYSTEM`, `HOME`, `TMPDIR`, the reserved Codex `TOKEN` variable, and `KANDEV_GITHUB_CREDENTIAL_BROKER_URL`. The injected set lives in `internal/agent/sandboxruntime/environment.go` and is the single source for the report. An allowlist name that collides with an injected name is ignored and reported `shadowed`. The report holds `environment.missing` (names only, sorted ascending, empty list when none) and no values.

## Agent exit diagnosis (012.2)

Core computes at admission and passes to agentctl with the start request: `executable_readable` and the agent's `missing_markers`. Agentctl keeps the last 2 KiB of stderr, drains it for up to 100 ms after exit, ignores output after the decision, redacts it with the existing log redaction, and on a pre-handshake exit or an ACP initialize timeout chooses in order: `agent_binary_denied` if `executable_readable` is false (skipped when the hint is absent); `agent_credentials_missing` if the tail contains a marker; `sandbox_denied_other` if it contains a fixed denial token kept in agentctl (`Operation not permitted`, `Permission denied`, `EPERM`, `EACCES`); `agent_handshake_timeout` for a timeout with no exit; else `agent_exited`. The decision is taken once per attempt under a mutex; exit wins if both are ready. Agentctl discards the tail and sends core only the cause, persisted as `launch_cause`. A persist failure logs WARN, the live error still shows the cause, and item 012.2 becomes `unknown` `evidence_unavailable`.

## REST auth and bind (012.6, 012.16)

Agentctl already authenticates routes with `instanceAuth` (`agentctl/server/api/auth.go`), which passes everything through when there is no static token and no credential source, and binds by `AGENTCTL_LISTEN_HOST` / `agent.standaloneHost` (effective default 127.0.0.1). The delta, owned by Task 17A unless noted: a test that enumerates registered routes against the exemption list of 012.6; identical 401 bodies and constant-time comparison for every bad-credential case; `/identity` fields `bind` (computed from the real `net.Listener` address) and `rest_auth` (`enforced` or `none`); with the loopback flag on, the launcher forces loopback unless `agent.standaloneHost` is set; the code-server process (`agentctl/server/process/vscode.go`, currently `0.0.0.0`) binds loopback when the create request says the attempt is sandboxed. The admission refusals `sandbox_bind_exposed` (`bind_exposed`; `bind_unreported` when `/identity` is unreadable in one 5 s attempt) and `sandbox_rest_auth_unenforced` are Task 19. The listener host is per agentctl process; instance listeners use it process-wide. `server.host` is the backend's own key and is untouched.

## Process surface (012.8)

A new type `SandboxProcessKind` in `internal/agent/sandbox`. The kind is passed explicitly at every call site and is never inferred from `streams.ProcessKind`.

| Kind | Source | Disposition |
| --- | --- | --- |
| `setup_script`, `cleanup_script`, `custom_script`, `dev_process` | scriptengine | `unsandboxed` |
| `language_server` | LSP manager | `unsandboxed` |
| `agent_terminal` | agent terminal transport | sandboxed; refused on darwin with `sandbox_transport_unsupported` |
| `user_terminal` | operator-opened shell | `unsandboxed` |
| `code_server` | `process/vscode.go` | `unsandboxed`, loopback bind |
| `helper` | MCP relay start (`lifecycle/session_mcp_codex.go`), broker reachability (`lifecycle/broker_reachability.go`), managed Git runs | labelled `unsandboxed`, does not degrade 012.8 |

`ProcessKindCustom` is also used by the helper starts, which is why the kind is explicit. Every Kandev process start in lifecycle, scriptengine and agentctl process management passes through one entry that takes the kind and the attempt, writes a label with outcome `pending`, starts the process, then records `started` or `failed`. A failed start does not degrade 012.8; a crash that leaves `pending` counts as started. Each start request gets its own label. A process shared by several attempts of one task is labelled by the starting attempt and shown with that label to the others. A label write failure aborts the start with `process_label_unrecorded`. The temp-root part of `state_root_denied` is darwin-only (`sandbox_state_root.go`); symlink traversal applies everywhere. A test using the Go AST fails when a process start appears outside the entry and an allowlist, and checker canaries use test-fixture source files, with no test-only hook in production code.

## Preview origin (012.9)

Covers the HTML preview and the session port proxy; the browser-facing proxy is `gateway/websocket/port_proxy.go`, with `agentctl/server/api/workspace_preview.go` as the publish handler. The proxy answers on `<task-token>.<preview.baseDomain>`, a new config key. Isolation applies to any task with a task guard or any sandboxed attempt ever, whether or not an attempt is live and for every session of the task.

**Token storage.** Table `task_preview_origins(task_id PRIMARY KEY, token, created_at)`. The token (256-bit random) is created lazily with `INSERT ... ON CONFLICT DO NOTHING` then `SELECT`, deleted with the task, and never reissued.

**Auth handoff.** An authenticated UI calls `POST /api/v1/tasks/:id/preview-ticket`, which returns a preview-origin URL carrying a single-use 256-bit ticket (TTL 60 s). The preview origin exchanges it for a host-only cookie scoped to that task's preview routes. A reused or expired ticket gets 403.

**Disabled and error contract.** With no base domain configured (desktop, loopback installs) or the flag off: HTTP 403 `preview_origin_unavailable`, WebSocket upgrades refused with the same 403, and the UI hides the control and shows the reason. A token-store failure gives 503 with the same code. A task deleted mid-session gives 404 and its WebSocket is closed. `preview.baseDomain` must differ from the UI host and must not be its parent domain, and UI cookies must be host-only; both are asserted by tests. Redirects keep the preview origin and requests keep the existing capability check. This amends ADR-2026-09-05 for sandboxed sessions only; see the ADR `2026-10-26-sandboxed-preview-origin`.

## Child ceiling (012.15)

Task creation inserts the child in one transaction that first takes a lock on the parent task row (`SELECT ... FOR UPDATE`; SQLite serializes writes) and reads the parent guard, then applies the same containment test as `checkParentGuard` (`agent/sandbox/task_admission.go`, using `noBroader` and `WithinCeiling`) over every guard dimension. A nil request inherits exactly; an explicit empty grant list is allowed. The guard update takes the same lock, so each creation sees the guard as committed at its own commit. `sandbox_outside_ceiling` is a new typed error. The launch check stays as defense in depth.

## Conformance suite (012.13)

**Shape.** A Go test binary `cmd/sandbox-conformance` plus a shell wrapper. It drives an isolated Kandev instance (temporary data directory under the user home, OS-assigned port, never the default instance) through the REST API with a real agent profile, and records JSON results. It supersedes the vocabulary of the AC-AGENTS-SANDBOX-011.11 darwin suite: that suite stays as a lower-level harness whose canaries the new suite wraps and maps (`allowed` canaries are not wrapped; `denied` becomes the probe with its control; `control_failed` becomes `invalid`; `skipped` becomes `not_applicable` only with a host-level reason, otherwise `error`).

**Journey.** Scripted, in order: enable flag, install provider package, create an org-scoped policy, bind to the executor, check availability, fault step (a second harness-started instance with its data directory under a temporary root, expect `state_root_denied` there and the main instance unaffected), fault step (agent binary outside the grant, expect `agent_binary_denied`), create a task with the agent, send a prompt and read the reply, open a native terminal (expect `sandbox_transport_unsupported` on macOS), call a Kandev MCP tool, run `git commit` and `git push` to a local bare fixture remote through the broker, trigger a denial and read it in the snapshot and report, request and answer an approval, restart the backend mid-session with `features.agentSurvival` off and resume, cancel, clean up. Each step is `passed`, `failed` or `not_applicable`. Every step has a 120 s bound and the journey 20 min; a poll never retries a failed assertion. Restart asserts a replacement attempt with the same policy revision and the same item states; cancel asserts the task state and that no owned process remains; cleanup asserts the attempt root and the instance data directory are gone. Step `not_applicable` reasons come from the closed list: `host_os`, `approvals_unsupported_host`, `transport_unsupported`, `git_identity_excluded`, `git_common_dir_excluded`, `executor_not_in_cell`, `flag_dependency`. On macOS the commit and push step is `git_identity_excluded` (Local) or `git_common_dir_excluded` (Worktree); the broker relay is therefore verified on Linux only in this delivery.

**Canaries.** Each has a probe and a control, of a declared control kind: `sandbox` (the probe runs unsandboxed in the same fixture and must succeed), `server` (the probe repeats with the valid credential and must succeed), `unmanaged` (plain Git outside managed Git executes the fixture code), `opt_in` (a hooks-enabled repository runs the hook), `replacement` (a new attempt picks the new revision), `checker` (the AST checker flags an unlabelled start in a test-fixture source file). Results are `contained`, `escaped`, `invalid`, `not_applicable` or `error`. An absent target on a host that lacks it gives `not_applicable` with reason `host_os`; a missing fixture on a host that should have it gives `error`. A canary passes only as `contained` with a valid control. `G2_finished_token` treats 401, 503 `mcp_token_unconfigured` and a refused connection as `contained`, with the valid token succeeding before the attempt ends as the control; `error` is only for a timeout or crash. Fixture names are generated markers; only marker ids are printed.

| Canary | AC | Control kind |
| --- | --- | --- |
| `G1_child_widen`, `G1_child_concurrent` | 012.15 | `server`: in-ceiling child created. Escape for the concurrent one: a child row committed wider than the guard committed before its insert |
| `G1_mcp_unguarded_tool` | 012.4 | `server`: same tool from an unguarded session |
| `G1_pin_policy_change` | 012.3 | `replacement` |
| `G2_no_token`, `G2_wrong_token`, `G2_finished_token` | 012.5 | `server` |
| `G2_rest_no_credential` | 012.6 | `server` |
| `G2_nonloopback_bind` | 012.16 | `server`: loopback instance admitted |
| `G3_repo_fsmonitor`, `G3_repo_filter` | 012.7 | `unmanaged` |
| `G3_repo_hook` | 012.7 | `opt_in` |
| `G3_process_label_<kind>` per kind | 012.8 | `checker` |
| `G4_preview_cookie_read`, `G4_preview_storage_read` | 012.9 | `sandbox`-style: a page on the UI origin reads the marker |
| `G5_env_not_allowlisted`, `G5_env_absent_not_empty` | 012.10 | `sandbox`: unsandboxed child sees the marker variable |
| `G6_keychain_read` (darwin) | 012.11 | `sandbox`: unsandboxed read of a fixture keychain item |
| `G6_credential_file_outside_state` | 012.11 | `sandbox`: unsandboxed read of the file |
| `G6_launch_refused_missing` | 012.11 | `server`: launch with the method satisfied is admitted |
| `G7_executor_unsupported` | 012.12 | `server`: Local launch admitted |

**Cells and agents.** A cell is the tuple agent, host OS, executor (Local or Worktree). A canary counts as passing only as `contained`; `not_applicable` is counted separately. A cell passes when every journey step is `passed` or `not_applicable` and every applicable canary is `contained`, with none `escaped`, `invalid` or `error`.

**Where it runs.** A self-hosted runner per host OS, in a workflow triggered manually and on a schedule, never on forks, with a concurrency group per runner so runs never overlap. A trap removes the isolated instances and their data directories on cancel. Agent credentials come from the runner's secret store and reach the agent only through the policy's environment keys or the provisioned agent state; the workflow never echoes them.

**Result record.** Each run writes a JSON artifact (suite version, commit, `scope_hash`, host OS, agent type, executor, `result` of `full`, `partial` or `failed`, step and canary results, counts) containing no path outside the fixture root and no environment value. `sandbox-conformance --acceptance-line <artifact>` prints one line a maintainer appends to `acceptance.md` in a pull request; CI does not commit:

`conformance <suite_version> <commit> <scope_hash> <os>/<agent>/<executor> <full|partial|failed> <sha256>`

Reruns append; concurrent pull requests that append keep both lines. `scope_hash` is the SHA-256 of the files listed in `docs/plans/nono-sandbox-integration/conformance-scope.txt` (the paths implementing 012.1 to 012.16). `sandbox-conformance --verify-release <sha>` reads `acceptance.md` and passes a cell only when the last line for that cell with the current `scope_hash` is `full`, on a clean tree. A run on a dirty tree or without a real agent is `partial` and satisfies nothing.

## Rollout

| Flag | Default | Restart | Purpose |
| --- | --- | --- | --- |
| `features.sandboxPolicies` | off in prod, dev and e2e | yes | enables providers, policies, operator routes |
| `features.sandboxSessionSummary` | off | no | summary, refusal records, new refusals and reason codes, labels, exit causes |
| `features.agentctlLoopbackBind` | off | yes | loopback default for sandboxed instances and its enforcement |
| `features.previewSeparateOrigin` | off | yes | per-task preview origins |

The three new flags are registered by Task 24 through the runtime flag registry with the profile and frontend contract tests. The two dependent flags are consulted only while the summary flag is on (see the requirement). Settings availability gains `executors: [{executor_type, supported, reason_code}]` from a static core table that `prepareExecution` also uses. Promotion order: summary, then loopback bind, then preview origin, each turned on in `dev` first. `features.sandboxPolicies` moves to `prod: true` only when, for the commit being released, `--verify-release` passes for the six required cells (Claude Code and Codex on Linux, and Claude Code on macOS, each on Local and Worktree), with no `invalid` or `error` canary, and the three new flags are on in `prod`. A change to any file in `conformance-scope.txt` changes `scope_hash` and invalidates earlier results. While any cell is missing the feature stays experimental with the risk text unchanged.

## Failure handling summary

A refusal is a launch outcome, not a crash. Registry, bind and diagnosis failures degrade to the most restrictive result: an unknown credential refuses, an unreported bind refuses, an unknown host capability refuses with `sandbox_host_unsupported`. No check falls back to allowing a launch, except the bind check with its flag off, which is reported `degraded` `feature_disabled`.
