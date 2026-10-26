---
status: draft
system: agents
created: 2026-10-26
requirements:
  - REQ-AGENTS-SANDBOX-012
---

# Sandboxed session system design

Extends [Sandbox policy system design](sandbox-policies.md), which owns policy, admission, attempts and reports. This file owns the end-to-end session contract, the launch-state summary, and the conformance suite that gates release. Ownership of work orders and the order of delivery are in the [plan](../../../plans/nono-sandbox-integration/plan.md). Evidence recompute, journey states, darwin result mapping and release-record mechanisms are in the [companion design](sandbox-session-conformance-evidence.md).

## Requirement-to-mechanism map

| AC | Mechanism | Work order |
| --- | --- | --- |
| 012.1 | `newSandboxRuntime` returns a typed reason; the backend installs a refusing runtime instead of exiting; availability gains `reason_code` | 19 |
| 012.2 | Agentctl maps exit status and a stderr tail to a closed cause using core-supplied hints; only the cause leaves agentctl | 19, 15 |
| 012.3 | Pinned attempt revision (existing); admission re-run on new-process resume, recovery and replacement | 04, 19 |
| 012.4 | Guarded MCP surface; canary only | 11, 23 |
| 012.5 | Per-instance MCP token and broker relay (REQ-EXECUTORS-SESSION-CONTROL-ISOLATION-001/002) | 12, 21, 23, 24 (teardown retries) |
| 012.6 | Route enumeration tests (instance router, and control router against its own list), fixed exemption list on the existing `instanceAuth`, `rest_auth` check at admission | 17, 19 |
| 012.7 | Managed Git isolation; canary only | 13, 21, 23 |
| 012.8 | One start entry taking `SandboxProcessKind`; labels in the snapshot | 18 |
| 012.9 | `preview_state_changed` committed on the attempt before the first preview byte is sent; item `degraded` `preview_same_origin`; failed commit refuses the request | 18 (firing sites, persisted state, refusal, tests), 24 (event handling) |
| 012.10 | Existing `EnvironmentKeys` enforcement; name-only reporting; empty values absent on every host | 04, 19, 21 |
| 012.11 | Credential registry consulted in `prepareExecution` after environment resolution | 15 |
| 012.12 | Executor-type capability table | 19 |
| 012.13 | Conformance suite and canary set | 20, 23 |
| 012.14 | Attempt-derived summary in the execution snapshot and Execution details | 24, 19, 10 |
| 012.16 | Bind reported in `/identity`, checked in `prepareExecution`; launcher default | 17, 19 |

## Session summary state

The execution snapshot (Task 10) gains `sandbox_summary`: `confined`, `degraded` or `refused`, plus `items`, a list of `{ac, state, reason}`. The item set is fixed, one item per guarantee AC (012.1 to 012.12 and 012.16; 012.13 and 012.14 are not items), never duplicated; clients ignore ids they do not know. State is one of `holds`, `degraded`, `not_applicable`, `unknown`, `refused`. `degraded` and `unknown` prevent `confined`. A session with a setup script labelled `unsandboxed` is therefore `degraded` in the first delivery, which is the intended honest result.

`reason` is required for every state except `holds` (where an informational reason such as `repo_opted_in` is allowed). `not_applicable` always carries one of `no_guard`, `handshake_pending`, `clean_handshake`, `host_os`.

A refusing item shows `refused` with its code; items evaluated before it in the evaluation order show their evaluated state (`holds`, or `degraded` for 012.16 with its flag off); every other item (later checks, runtime items, and 012.3, 012.4, 012.5, 012.7) shows `unknown` with reason `not_evaluated`. Item 012.6 with `rest_auth` explicitly `none` is `refused` with reason `rest_auth_none`.

| Item | Applies | `holds` when | Otherwise |
| --- | --- | --- | --- |
| 012.1 | always | runtime available at admission | `refused` |
| 012.2 | always | never `holds`: `not_applicable` `handshake_pending`, then `clean_handshake` | `degraded` with the cause code |
| 012.3 | always | revision pinned | `unknown` `evidence_unavailable` |
| 012.4 | pinned attempt carries a task guard | the attempt's `guard_enforced` is true (written at admission by the Task 21 wiring: guard present and the server-side class table registered for the session's MCP endpoint) | `unknown` `evidence_unavailable`; `not_applicable` `no_guard` |
| 012.5 | always | the instance `/identity` carries `mcp_token_fingerprint` non-empty, or core's revocation record says the revocation was core's own at attempt end | `degraded` `token_revoked` when the fingerprint is empty and core has no own-revocation record for the attempt; `unknown` `token_unsupported` when the field is absent; `unknown` `evidence_unavailable` when the instance cannot be read |
| 012.6 | always | `/identity` `rest_auth` is `enforced` | `refused` `sandbox_rest_auth_unenforced` |
| 012.7 | always | the attempt's `git_isolation_active` is true (written at admission by the Task 21 wiring); `holds` with `repo_opted_in` when the repo enabled hooks | `unknown` `evidence_unavailable` |
| 012.8 | always | every Kandev-started process of this attempt so far is sandboxed or a `helper` (an `unsandboxed` label is sticky for the attempt; closing the process does not restore it) | `degraded` `process_unsandboxed` |
| 012.9 | always | no preview served for this attempt | `degraded` `preview_same_origin` |
| 012.10 | always | environment report present | `unknown` `evidence_unavailable` |
| 012.11 | always | a registry method satisfied | `refused` |
| 012.12 | always | executor type supported | `refused` |
| 012.16 | always | instance bind loopback | `refused`; `degraded` `feature_disabled` when the flag is off |

The reason set is closed: the codes named in the requirement plus `feature_disabled`, `repo_opted_in`, `process_unsandboxed`, `evidence_unavailable`, `not_evaluated`, `token_revoked`, `no_guard`, `handshake_pending`, `clean_handshake`, `host_os`, `rest_auth_unreported`, `rest_auth_none`, `token_unsupported`, `preview_same_origin`, `identity_unreadable`.

**Events.** Every event reaches the attempt through one serialization point in core, which assigns the per-attempt sequence number on receipt, under a mutex; order is receipt order. The summary is computed at admission and recomputed only by these events and the summary-flag toggle (a recorded latch event, see Launch details): `attempt_admitted`, `process_started` (fired when the `pending` label is committed, before the process starts; a failed start fires `process_start_failed`, which drops the label), `preview_state_changed` (fired by core before the first preview byte is sent, from the preview publish handler `task/handlers/workspace_preview_handlers.go` and the session port proxy `gateway/websocket/port_proxy.go`; the state is committed on the attempt first, in the column `preview_served_at`, and a failed commit refuses the request with `preview_state_unrecorded` instead of serving it), `handshake_completed` (012.2 becomes `not_applicable` `clean_handshake`), `exit_diagnosed`, `token_revoked`, `git_policy_changed` (the hooks setting changed, recomputes 012.7), `evidence_lost`, `evidence_restored`, `attempt_ended`. Evidence polling, `evidence_lost` and `evidence_restored` are in the companion design. `attempt_ended` is emitted wherever the attempt row is ended, including the startup reconciliation of attempts whose instance is not adopted (so a restart with `features.agentSurvival` off still freezes the predecessor). Teardown records core's own revocation before it revokes, so `token_revoked` is never raised for it. `attempt_ended` is emitted after the exit report of the agent process or 5 s after the process is seen gone, whichever is first; a diagnosis or process report that arrives later is persisted on the attempt (`launch_cause`, labels) and shown in Execution details, and the frozen summary is not recomputed. After `attempt_ended` the summary is frozen.

**Indicator matrix** (first match wins; the web client renders from the snapshot and derives no state):

| Status / summary | Indicator |
| --- | --- |
| summary `refused`, or status `failed` | failed |
| summary `degraded`, any status other than `failed` | warning |
| status `active`, summary `confined` | shield |
| anything else (including sessions without a summary) | the existing 008.3 rules |

On WebSocket loss the client hides the shield and shows "status unavailable", as the inherited projection does. Every reason code has localized text and a remedy in seven locales.

## Launch refusal record

A table `sandbox_launch_refusals` (`session_id`, `refusal_id`, `items`, `summary`, `refusal_code`, `created_at`) separate from `sandbox_attempts`. It is written only while `features.sandboxSessionSummary` is on, one row per refused launch request, and pruned to the latest 20 per session on write (order `created_at` desc, then `refusal_id` desc). No attempt reader sees it. If policy resolution fails every item is `unknown` `not_evaluated` and the refusal code is the policy error class. The current attempt shown is the live attempt when there is one, else the newer of `LatestSessionAttempt` and the latest refusal by creation time then id; a refusal written while an attempt is live is shown as the latest refused launch only. If the write fails the launch error code still reaches the client, which shows the failed indicator and "details unavailable" plus the code in Execution details.

## Launch admission order

`sandboxLaunchAdmission.admit` resolves policy, then runs runtime availability (012.1). `prepareExecution`, where the executor type, the resolved launch environment and the instance `/identity` are known, then runs the executor capability table with the AC-AGENTS-SANDBOX-011.2 host test (012.12), the bind check (012.16), the `rest_auth` check (012.6), then the credential registry (012.11), all before runtime preparation. The order is exactly the requirement's evaluation order. The checks are an ordered registry in Task 24 that Tasks 15 and 19 plug into. Task 17 is agentctl and launcher only. The first refusal is returned and written to the refusal record; later items are `unknown` `not_evaluated`. Continuation on a live attempt runs none of them. A new-process resume, recovery or replacement re-runs them, keeps the pinned revision, writes a new attempt with `restart_of`, and on refusal keeps the conversation and writes a refusal record.

## Credential registry (012.11)

A table in `internal/agent/sandboxruntime` keyed by agent type and host platform, holding a list of methods `{kind, delivery}` (registry order, deduplicated) and a list of `missing_markers`. Kinds: `env_key` (names the variable the policy must allowlist), `file_in_agent_state` (a path relative to the `agent_state` grant), `host_keychain` (never satisfied), `none`. An entry with an empty method list is invalid and treated as unknown; a build test rejects it. The remedy shows the env key name and the relative path. `file_in_agent_state` is checked in `prepareExecution` against the session's existing agent state only; it never reads or copies from another location, and Kandev never writes credentials, so a first launch is satisfied by `env_key` alone in this version (seed provisioning is Out of scope). An `env_key` whose name is in the injected or core minimal set (see Child environment) is invalid; a build test rejects such an entry, so a delivered credential cannot be shadowed. Registry data holds names and paths only and is the single source for the admission check and the setup hint. Initial entries, provisional until measured on a real host and recorded in `acceptance.md`: `claude-acp` (`env_key` `ANTHROPIC_API_KEY`; `file_in_agent_state` for its credentials file; `host_keychain` on darwin), `codex-acp` (`env_key` `OPENAI_API_KEY`; `file_in_agent_state` for its auth file), and the e2e mock agent (`none`). Any other agent is `sandbox_credentials_unknown`, intended and listed in Out of scope.

## Child environment (012.10)

The child environment is the policy allowlist, plus the core minimal set, plus the Kandev-injected set. Both sets are core-owned in `internal/common/sandbox/runtime_environment.go` (`IsolatedEnvironment`; no second copy, no provider declaration). Task 19 exports them as one list that the report and the registry build test read.

| Set | Names | Value source |
| --- | --- | --- |
| minimal | `PATH`, `LANG`, `LC_ALL`, `TERM`, `COLORTERM`, `TZ` | the launch environment (profile and secrets, before the policy filter); absent when missing or empty, and not reported |
| injected | `HOME`, `XDG_CONFIG_HOME`, `XDG_STATE_HOME`, `TMPDIR` | the attempt's runtime directories |
| injected | `GIT_CONFIG_NOSYSTEM=1` | darwin only |
| injected | the managed broker contract: the names `githubauth.RemoveManagedCredentialEnvironment` removes (broker URL, helper path, lease, reissue capability, CLI shim and bash-env entries, task, session, repository, owner, repo, host and scope) and the Git configuration entries that register the managed helper (`GIT_CONFIG_COUNT`, `GIT_CONFIG_KEY_<n>`, `GIT_CONFIG_VALUE_<n>` of the entries `githubauth.IsManagedGitCredentialConfigEntry` identifies) | copied from the agent environment when the lifecycle configured the relay for the attempt (Task 21 wires it, from a single exported list in `githubauth`); absent otherwise |

An allowlist name that equals an injected name is ignored and reported `shadowed`; one that equals a minimal name has no effect and is not reported. An allowlisted name missing or empty in the launch environment is absent and reported `missing`. Empty values are dropped on every host: today `EffectiveEnvironmentValue` keeps an empty value on Linux and drops it on darwin, and Task 19 makes both drop it (a Linux launch that relied on an empty allowlisted variable now sees it absent, as 012.10 requires). The report holds `environment.missing` and `environment.shadowed` (names only, sorted ascending, empty lists when none) and no values.

## Agent exit diagnosis (012.2)

Core computes in `prepareExecution` and passes to agentctl with the start request: `executable_readable` (true only when the symlink-resolved agent executable lies inside a read grant of the pinned policy or inside the core-owned `SystemReadRoots` list per host OS, defined in `internal/common/sandbox`; no provider declaration is involved) and the agent's `missing_markers`. Agentctl keeps the last 2 KiB of stderr, drains it for up to 100 ms after exit, ignores output after the decision, redacts it with the existing log redaction, and on a pre-handshake exit or an ACP initialize timeout chooses in order: `agent_binary_denied` if `executable_readable` is false (skipped when the hint is absent); `agent_credentials_missing` if the tail contains a marker; `sandbox_denied_other` if it contains a fixed denial token kept in agentctl (`Operation not permitted`, `Permission denied`, `EPERM`, `EACCES`); `agent_handshake_timeout` for a timeout with no exit; else `agent_exited`. The decision is taken once per attempt under a mutex; exit wins if both are ready. Agentctl discards the tail and sends core only the cause, persisted as `launch_cause`. A persist failure logs WARN, the live error still shows the cause, and item 012.2 becomes `unknown` `evidence_unavailable`.

## REST auth and bind (012.6, 012.16)

Agentctl already authenticates routes with `instanceAuth` (`agentctl/server/api/auth.go`), which passes everything through when there is no static token and no credential source, and listens on `AGENTCTL_LISTEN_HOST`. Without that override it listens on all interfaces whenever an auth token is set (`Config.ListenHost`), but the managed launcher always sets it to `agent.standaloneHost` (`launcher.go`), the host the backend also dials (default `localhost`/`127.0.0.1`). A standalone agentctl started outside the launcher with a token and no override is therefore exposed, which 012.16 detects.

Agentctl has two routers with separate credential rules. The instance router (`server.go`) is what 012.6 governs. The control server (`control_server.go`) keeps `controlCredentialAuth` with its four exemptions and its superseded-credential rule on `adoptionOnlyPaths` (AC-EXECUTORS-CONTROL-OWNERSHIP-002.7 and -002.9); it is not changed and a second enumeration test holds it to that list. Exemption matching on the instance router uses the cleaned request path; a path whose cleaned form differs from the raw one is not exempt and gets 401, and `/git-credentials/` is a subtree match for every method.

The delta, owned by Task 17 unless noted: the enumeration tests; identical 401 bodies and constant-time comparison for every bad-credential case; three added fields on the authenticated instance `/identity` (the route the backend reads and the 10 s probe polls; the control `/identity` is unchanged): `bind`, `rest_auth` (`enforced` or `none`), and the existing `mcp_token_fingerprint`, which is the 012.5 evidence (empty means no active token). `bind` is `exposed` when the instance listener or the control listener is not loopback, computed from the real `net.Listener` addresses. The launcher is unchanged: `agent.standaloneHost` is the single key for dial and listen host. The code-server process (`agentctl/server/process/vscode.go`, currently `0.0.0.0`) binds loopback when the create request says the attempt is sandboxed and `features.agentctlLoopbackBind` is on; with the flag off it keeps its bind and is labelled `unsandboxed`.

The flag is enforcement only: no restart, no launcher change, consulted at admission and at the code-server create request, so it affects future launches and never a live instance. Admission refusals are Task 19: `sandbox_bind_exposed` (`bind_exposed` when `bind` is `exposed` with the flag on, or when the dial host is not loopback with the flag in either state, which the existing `protectedPorts` check in `backendapp/sandbox_runtime.go` reports as a generic ineligible error that Task 19 maps to this code; `bind_unreported` when `/identity` omits `bind`) and `sandbox_rest_auth_unenforced` (a `/identity` that omits `rest_auth`, as a retained older agentctl does, is `none` with reason `rest_auth_unreported` even when the bind flag is off). The listener host is per agentctl process. `server.host` is the backend's own key and is untouched.

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

`ProcessKindCustom` is also used by the helper starts, which is why the kind is explicit. Every Kandev process start in lifecycle, scriptengine and agentctl process management passes through one entry that takes the kind and the attempt, writes a label with outcome `pending`, starts the process, then records `started` or `failed`. A failed start does not degrade 012.8; a crash that leaves `pending` counts as started. Each start request gets its own label. A process shared by several attempts of one task is labelled by the starting attempt and shown with that label to the others. A label write failure aborts the start with `process_label_unrecorded`. Processes that agentctl starts on core's request (code-server, language servers, user shells) take their label from core: core writes `pending` before it calls agentctl, passes the label id, and agentctl reports `started` or `failed` through the existing process status events; a missing report leaves `pending`, counted as started. A process started after `attempt_ended` (a cleanup script) is labelled against the ended attempt id; the frozen summary is not recomputed and Execution details lists those labels separately. A `restart_of` attempt counts the labels of still-live shared processes of its predecessor plus its own. The temp-root part of `state_root_denied` is darwin-only (`sandbox_state_root.go`); symlink traversal applies everywhere. A test using the Go AST fails when a process start appears outside the entry and an allowlist, and checker canaries use test-fixture source files, with no test-only hook in production code.

## Conformance suite (012.13)

**Shape.** A Go test binary `cmd/sandbox-conformance` plus a shell wrapper. It drives an isolated Kandev instance (temporary data directory under the user home, OS-assigned port, never the default instance) through the REST API with a real agent profile, and records JSON results. It supersedes the vocabulary of the AC-AGENTS-SANDBOX-011.11 darwin suite, a lower-level harness whose canaries the new suite wraps; the full result mapping is in the companion design.

**Journey.** The harness starts every instance with `features.sandboxPolicies`, `sandboxSessionSummary` and `agentctlLoopbackBind` all true through environment overrides, and asserts the effective values before step one; a mismatch fails the journey and is never `not_applicable`. Scripted, in order: install provider package, create an org-scoped policy, bind to the executor, check availability, fault step (a second harness-started instance whose data directory is reached through a symlink on every host, and under a temporary root on darwin; expect `state_root_denied` there and the main instance unaffected), fault step (agent binary outside the grant, expect `agent_binary_denied`), create a task with the agent, send a prompt and read the reply, open a native terminal (`passed` on macOS when `sandbox_transport_unsupported` is returned), call a Kandev MCP tool, run `git commit` and `git push` to a local bare fixture remote through the broker, trigger a denial and read it in the snapshot and report, request and answer an approval, serve a preview of a fixture port (summary moves to `degraded` `preview_same_origin`), open a user terminal, restart the backend mid-session with `features.agentSurvival` off and resume, cancel, clean up. Each step is `passed`, `failed` or `not_applicable`; the expected summary and indicator after each step are in the companion design. Every step has a 120 s bound and the journey 20 min; a poll never retries a failed assertion. Restart asserts a replacement attempt with the same policy revision and the same state for every item except 012.2, 012.8 and 012.9 (012.8 follows the inheritance rule of the process surface; a replacement starts with no preview served); cancel asserts the task state and that no owned process remains; cleanup asserts the attempt root and the instance data directory are gone. Step `not_applicable` reasons come from the closed list: `host_os`, `approvals_unsupported_host`, `denial_report_unsupported_host`, `git_identity_excluded`, `git_common_dir_excluded`, `executor_not_in_cell`. On macOS the commit and push step is `git_identity_excluded` (Local) or `git_common_dir_excluded` (Worktree); the broker relay is therefore verified on Linux only in this delivery.

**Canaries.** Each has a probe and a control, of a declared control kind: `sandbox` (the probe runs unsandboxed in the same fixture and must succeed), `server` (the probe repeats with the valid credential and must succeed), `unmanaged` (plain Git outside managed Git executes the fixture code), `opt_in` (a hooks-enabled repository runs the hook), `replacement` (after a binding change, an operator-started launch resolves the new revision while the live attempt and its replacement keep the pinned one), `checker` (the AST checker flags an unlabelled start in a test-fixture source file). Results are `contained`, `escaped`, `invalid`, `not_applicable` or `error`. An absent target on a host that lacks it gives `not_applicable` with reason `host_os`; a missing fixture on a host that should have it gives `error`. A canary passes only as `contained` with a valid control. `G2_finished_token` replays the token after the attempt ends: 401, 503 `mcp_token_unconfigured` or a refused connection is `contained`, the valid token succeeding before the end is the control, and only a timeout or crash is `error`. Fixture names are generated markers; only marker ids are printed. Items without a canary are proved by journey steps with the same pass rule: 012.1 and 012.12 by the fault step and the unsupported-executor canary, 012.2 by the `agent_binary_denied` fault step and a cause assertion, 012.14 by reading the summary and indicator at each step.

| Canary | AC | Control kind |
| --- | --- | --- |
| `G1_mcp_unguarded_tool` | 012.4 | `server`: same tool from an unguarded session |
| `G1_pin_policy_change` | 012.3 | `replacement` |
| `G2_no_token`, `G2_wrong_token`, `G2_finished_token` | 012.5 | `server` |
| `G2_rest_no_credential` | 012.6 | `server` |
| `G2_nonloopback_bind` | 012.16 | `server`: loopback instance admitted |
| `G3_repo_fsmonitor`, `G3_repo_filter` | 012.7 | `unmanaged` |
| `G3_repo_hook` | 012.7 | `opt_in` |
| `G3_process_label_<kind>` per kind | 012.8 | `checker` |
| `G5_env_not_allowlisted`, `G5_env_absent_not_empty` | 012.10 | `sandbox`: unsandboxed child sees the marker variable |
| `G6_keychain_read` (darwin) | 012.11 | `sandbox`: unsandboxed read of a fixture keychain item |
| `G6_credential_file_outside_state` | 012.11 | `sandbox`: unsandboxed read of the file |
| `G6_launch_refused_missing` | 012.11 | `server`: launch with the method satisfied is admitted |
| `G7_executor_unsupported` | 012.12 | `server`: Local launch admitted |

**Cells and agents.** A cell is the tuple agent, host OS, executor (Local or Worktree). A canary counts as passing only as `contained`; `not_applicable` is counted separately. A cell passes when every journey step is `passed` or `not_applicable` and every applicable canary is `contained`, with none `escaped`, `invalid` or `error`.

**Where it runs.** A self-hosted runner per host OS, in a workflow triggered manually and on a schedule, never on forks, with a concurrency group per runner so runs never overlap. A trap removes the isolated instances and their data directories on cancel. Agent credentials come from the runner's secret store and reach the agent only through the policy's environment keys; the workflow never echoes them.

**Result record.** Each run writes a JSON artifact (suite version, commit, `scope_hash`, host OS, agent type, executor, `result` of `full`, `partial` or `failed`, step and canary results, counts) containing no path outside the fixture root and no environment value. `sandbox-conformance --acceptance-line <artifact>` prints one line a maintainer appends to `acceptance.md` in a pull request; CI does not commit:

`conformance <suite_version> <commit> <scope_hash> <os>/<agent>/<executor> <full|partial|failed> <sha256>`

Reruns append; concurrent pull requests that append keep both lines. `scope_hash` is computed over the files listed in `docs/plans/nono-sandbox-integration/conformance-scope.txt` (the paths implementing 012.1 to 012.14 and 012.16) by the algorithm in the companion design. `sandbox-conformance --verify-release <sha>` reads `acceptance.md` and passes a cell only when the last line for that cell has `scope_hash` equal to the hash computed at `<sha>`, a `commit` that is `<sha>` or its ancestor, result `full`, and a `<sha256>` equal to the digest of the artifact committed beside it as `docs/plans/nono-sandbox-integration/conformance/<sha256>.json`; the pull request adding both is the review. `profiles.yaml` is not in `conformance-scope.txt`, so the promotion commit keeps the hash; `--verify-release` also fails unless the three flags of the suite's flag set are `prod: true` at `<sha>`, where `<sha>` is the head under verification (so the promotion pull request verifies itself). `--verify-release` runs on the promotion pull request head and on a schedule against the default branch. A run on a dirty tree or without a real agent is `partial` and satisfies nothing.

## Rollout

| Flag | Default | Restart | Purpose |
| --- | --- | --- | --- |
| `features.sandboxPolicies` | off in prod, dev and e2e | yes | enables providers, policies, operator routes |
| `features.sandboxSessionSummary` | off | no | summary, refusal records, new refusals and reason codes, labels, exit causes |
| `features.agentctlLoopbackBind` | off | no | enforcement at admission and for the code-server bind |

The two new flags are registered by Task 24 through the runtime flag registry with the profile and frontend contract tests. The bind flag is consulted only while the summary flag is on (see the requirement). Settings availability gains `executors: [{executor_type, supported, reason_code}]` from a static core table that `prepareExecution` also uses. Promotion: the two new flags are turned on in `dev` one at a time (summary, then loopback bind). The `prod` change is one pull request that sets `prod: true` for `features.sandboxPolicies` and the two new flags together; its required check runs `--verify-release` on the pull request head, which passes only when the six required cells (Claude Code and Codex on Linux, Claude Code on macOS, each on Local and Worktree) are recorded with no `invalid` or `error` canary and all three flags are `prod: true` at that head. A change to any file in `conformance-scope.txt` changes `scope_hash` and invalidates earlier results. While any cell is missing the feature stays experimental with the risk text unchanged.

## Launch details

A launch with no session (quick chat, host-utility probes sharing `admit` through `sandbox_utility.go`) returns the refusal as an error and writes no refusal record. The `executor_unsupported` part of 012.12 needs only the executor type and runs before any instance exists, so a Docker, SSH or Kubernetes launch creates nothing before its refusal. The remaining checks need the instance `/identity`, so a Local or Worktree instance is created first; that creates a listener and a token, not an agent process, provider compile or runtime root. `/identity` is read once per launch with a 5 s bound and the result is shared by the 011.2 host test, 012.16 and 012.6; an absent field is unreported (`bind_unreported`, `rest_auth_unreported`), and a timeout, non-200 or malformed body refuses with `sandbox_identity_unreadable`/`identity_unreadable`. A refusal revokes the token, then deletes the instance, and attempts the delete even when the revoke failed; a failure of either is logged at ERROR and the refusal is still returned. An instance whose delete failed is kept for the normal executor teardown, and one whose revoke and delete both failed is recorded as a row (`instance_id`, `created_at`) in table `sandbox_teardown_pending`, deleted on success, that is ungated by the summary flag and retried (revoke, then delete) by the executor teardown, the startup reconciliation and, while the backend runs, a 60 s timer limited to 10 timer attempts per row per run, logging ERROR on each failure. A refused launch starts no agent process, so no agent has seen that token; until a retry succeeds the instance is reachable only by core, which alone holds the token. The same table and retry apply to the teardown at attempt end (012.5); Task 24 owns the table and both retries. A toggle of `features.sandboxSessionSummary` to off during a live attempt stops its events and sets the `evidence_gap` latch of the evidence design for every runtime item at the toggle, at backend start and at adoption of a live attempt while the flag is off, so those items stay `unknown` `evidence_unavailable` for the rest of that attempt and a later toggle on never shows `confined`.

## Failure handling summary

A refusal is a launch outcome, not a crash. Registry, bind and diagnosis failures degrade to the most restrictive result: an unknown credential refuses, an unreported bind or unreadable identity refuses, an unknown host capability refuses with `sandbox_host_unsupported`. No check falls back to allowing a launch, except the bind check with its flag off, which is reported `degraded` `feature_disabled`.
