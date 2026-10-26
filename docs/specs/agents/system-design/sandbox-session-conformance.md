---
status: draft
system: agents
created: 2026-10-26
requirements:
  - REQ-AGENTS-SANDBOX-012
---

# Sandboxed session system design

Extends [Sandbox policy system design](sandbox-policies.md), which owns policy, admission, attempts and reports. This file owns the end-to-end session contract, the launch-state summary, and the conformance suite that gates release.

## Requirement-to-mechanism map

| AC | Mechanism | Owner work order |
| --- | --- | --- |
| 012.1 | `newSandboxRuntime` returns a typed reason; `main.go` records it and installs a refusing admission instead of returning false; availability API carries the code | Task 19 |
| 012.2 | Agentctl reports exit status plus a redacted 2 KiB stderr tail to core at pre-handshake exit; core maps it to the closed cause set | Task 19 |
| 012.3 | Existing task guards and ceilings (Tasks 04, 11); canary only | Task 20 |
| 012.4 | Guarded MCP surface (Task 11); canary only | Task 20 |
| 012.5 | Per-session MCP token and broker relay (Task 12) | Task 12, 21 |
| 012.6 | Instance credential on agentctl REST plus loopback bind selection | Task 17 |
| 012.7 | Managed Git isolation (Task 13) | Task 13, 21 |
| 012.8 | Process-kind labelling in the launch snapshot, sandboxed launch where the transport allows | Task 18 |
| 012.9 | Per-task preview origin | Task 16 |
| 012.10 | Environment allowlist enforced in the launch envelope (existing, `EnvironmentKeys`) plus name-only reporting | Task 20 (probe), Task 04 |
| 012.11 | Agent credential registry consulted at admission | Task 15 |
| 012.12 | Executor-type capability table; remote types report unsupported | Task 19 |
| 012.13 | Conformance suite | Task 20 |
| 012.14 | Attempt-derived summary state in the execution snapshot and Execution details | Task 19 |

## Session summary state

The execution snapshot (Task 10) gains `sandbox_summary`: `confined`, `degraded` or `refused`, plus an `items` list of `{ac, state, reason}` where state is `holds`, `degraded`, `unsupported` or `unknown`. The summary is computed once at admission from the pinned attempt and updated only by attempt events, never by a settings read. `confined` requires every applicable item to be `holds`. An item the host cannot assess is `unknown`, which is not `holds`, so the summary is `degraded`. The web client renders the summary and reason codes from the snapshot only; it derives no state itself. All copy is localized in seven locales.

## Launch admission order

`sandboxLaunchAdmission.admit` already resolves policy. The new checks run after policy resolution and before runtime preparation, in AC order: runtime availability (012.1), credentials registry (012.11), bind exposure (012.6), host/executor capability (012.12). The first refusal is returned to the caller; every unmet item is persisted on the attempt record that Task 10 captures. A refusal creates a failed bootstrap attempt that the existing recovery path (Task 02) can retry.

## Credential registry (012.11)

A table in `internal/agent/sandboxruntime` keyed by agent type and host platform: `{kind, delivery}`. `env_key` names the environment variable the policy must allowlist; `file_in_agent_state` names a path relative to the `agent_state` grant that the operator provisions inside the isolated home; `host_keychain` is always unsupported under a sandbox. Registry data holds names and paths only. The registry is the single source for the admission check and for the setup hint shown with the refusal. Entries ship for `claude-acp` and `codex-acp` first; other agents are refused as `sandbox_credentials_unknown` until added.

## Agent exit diagnosis (012.2)

Agentctl already supervises the sandbox owner. At pre-handshake exit it sends core `{exit_status, signal, stderr_tail}` where the tail is the last 2 KiB passed through the existing redaction used for logs. Core maps: stderr matching a denial marker from the provider's declared marker list to `agent_binary_denied` or `sandbox_denied_other` by whether the denied path is the agent's resolved executable or its install root; an exit with a missing-credential marker from the registry to `agent_credentials_missing`; anything else to `agent_exited`. The tail is discarded after mapping. The same mapping is unit tested with provider fixture output.

## Executor REST auth and bind (012.6)

Agentctl routes sit behind a middleware that requires the instance credential core already holds for control calls. Bind selection: a `--bind` setting with `loopback` (default for sandboxed Local and Worktree) and `all`. Admission reads the instance's reported bind and refuses with `sandbox_bind_exposed` when a sandboxed attempt would run on a non-loopback instance. Unsandboxed executors keep their present bind.

## Process surface (012.8)

Every process start in lifecycle passes through one function that takes `ProcessKind` (`setup_script`, `cleanup_script`, `dev_process`, `language_server`, `terminal`) and the attempt, and returns either a sandboxed launch or an `unsandboxed` label recorded on the snapshot before the process starts. Setup and cleanup scripts are labelled `unsandboxed` in the first delivery; sandboxing them is a follow-up that needs a second, wider policy. Terminals reuse the existing guarded-terminal refusal on darwin.

## Preview origin (012.9)

The preview proxy answers on a per-task origin of the form `<task-token>.<preview-base>`, with a distinct cookie jar from the UI. Where the deployment cannot provide wildcard names (no preview base configured, or loopback-only), previews are disabled with `preview_origin_unavailable`. The UI hides the preview control and shows the reason.

## Conformance suite (012.13)

**Shape.** A Go test binary `cmd/sandbox-conformance` plus a shell wrapper. It drives an isolated Kandev instance (temporary data dir under the user home, never the default instance, never a shared port) through the REST API, with a real agent profile, and records JSON results.

**Journey run.** Scripted, in order: enable flag, install provider package, create an org-scoped policy, bind to the Local executor, check availability, create a task with the agent, send a prompt and read the reply, open a native terminal (expected `sandbox_transport_unsupported` on macOS), call a Kandev MCP tool, run `git commit` and `git push` to a local bare fixture remote through the broker, trigger a denial and read it in the snapshot and report, request and answer an approval (Linux only; `not_applicable` on macOS), restart the backend mid-session and resume, cancel, and clean up. Each step is `passed`, `failed` or `not_applicable` with the reason. A `not_applicable` step needs a reason code from a closed list in the suite.

**Canaries.** Each escape has an id, a probe and a control. The control runs unsandboxed in the same fixture and must succeed (it proves the probe can see the escape). The probe runs inside the session and must be denied. Result states: `contained`, `escaped`, `invalid` (control did not detect). Escapes: `G1_child_widen`, `G1_mcp_unguarded_tool`, `G2_wrong_token`, `G2_rest_no_credential`, `G2_nonloopback_bind`, `G3_repo_hook`, `G3_repo_fsmonitor`, `G3_repo_filter`, `G3_script_unlabelled`, `G4_preview_cookie_read`, `G5_env_not_allowlisted`, `G6_keychain_read`, `G6_credential_file_outside_state`, `G7_remote_unprobed_launch`. Fixture files live under a generated root; canaries name files by generated marker names, and the suite prints marker ids only.

**Agents and hosts.** Claude Code and Codex, each on a real Linux host and a real Apple Silicon host. A matrix cell is the tuple agent, host OS, executor (Local or Worktree). A cell passes only when every journey step is `passed` or `not_applicable`, every canary is `contained`, and no canary is `invalid`.

**Where it runs.** A self-hosted runner per host OS, in a workflow triggered manually and on a schedule, never on forks. Agent credentials for the run come from the runner's secret store and reach the agent only through the policy's environment keys or the provisioned agent state; the workflow never echoes them.

**Result record.** Each run writes a JSON artifact (suite version, commit, host OS, agent type, step and canary results, invalid count) and appends one line to the acceptance record. The artifact contains no file paths outside the fixture root and no environment values. A run on a dirty tree or without a real agent is labelled `partial` and cannot satisfy a promotion.

## Ownership and dependencies

zeval owns work orders 04 to 10 on #4769. We own 11 and above.

| Ours | Blocks on (zeval) | Why |
| --- | --- | --- |
| 11 guarded surface | 04, 06 | admission and approval seam |
| 12 token and broker | 07 | MCP route and relay |
| 13 managed Git | none | independent of #4769 |
| 14 macOS | 05, 06, 07, 10 | provider, approvals, MCP route, surfaces (done) |
| 15 credential registry | 05 | needs the packaged provider and launch envelope |
| 16 preview origin | none | independent of #4769 |
| 17 REST auth and bind | none | agentctl-only, then 04 for admission |
| 18 process surface | 05, 10 | launch envelope and snapshot |
| 19 degraded states | 04, 10 | admission and snapshot |
| 20 conformance suite | 05, 07, 09 | provider, MCP route, acceptance harness |
| 21 standalone integration | 12, 13 plus the two standalone PRs | rebases our pieces onto their final shape |

**Order to a usable end-to-end path** (one agent, Local and Worktree, Linux and macOS): 05 provider lifecycle (zeval) so a real agent can launch; 19 so failures are visible; 15 so the agent can log in; 17; 18 labels; 20 journey run for one agent; then 16, remaining canaries, the second agent, and 21 once both standalone PRs have merged on main.

## Rollout

| Flag | Default | Purpose |
| --- | --- | --- |
| `features.sandboxPolicies` | off in prod, dev and e2e | #4769; enables providers, policies, operator routes; restart required |
| `features.sandboxSessionSummary` | off | shows `sandbox_summary` and enforces the new admission checks |
| `features.agentctlLoopbackBind` | off | selects loopback bind for sandboxed executors |
| `features.previewSeparateOrigin` | off | serves previews from per-task origins |

Each new flag is registered through the runtime flag registry with the profile and frontend contract tests. Promotion order: `sandboxSessionSummary` then loopback bind then preview origin, each turned on in `dev` first. `features.sandboxPolicies` moves to `prod: true` only when, for the commit being released, the conformance matrix has a passing cell for each of Claude Code and Codex on Linux, and Claude Code on macOS, with no `invalid` canary; and the three dependent flags are on. A change to any item of AC 012.1 to .12 invalidates earlier results and the matrix is rerun. While any cell is missing the feature stays experimental with the risk text unchanged.

## Failure handling summary

A refusal is a launch outcome, not a crash. Registry, probe and diagnosis failures degrade to the most restrictive result: unknown credential refuses, unknown bind refuses, unknown host capability is unsupported. No check falls back to allowing a launch.
